// migrate-legacy-minute 把旧 min-kline 分钟库（Kline 表或 KLM1/KLM2 压缩块）
// 转换为源时间分钟线写入 MySQL。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/injoyai/conv/cfg"
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/internal/marketstore"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/extend/pull"
)

type job struct {
	file string
	code model.Code
	year int
}

type result struct {
	job job
	receipt
	err error
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var (
		fromYear  = flag.Int("start-year", 0, "只导入该年起的年度库；0 表示全部")
		stdinDSN  = flag.Bool("config-stdin", false, "从标准输入读取私有 JSON DSN，不落盘或进入参数")
		dirArg    = flag.String("dir", "", "旧分钟库目录；默认 <pull.database>/min-kline，相对路径按当前目录解析")
		dsnArg    = flag.String("dsn", "", "目标 MySQL DSN；默认读取配置 pull.mysql，建议用 env 注入密码")
		workers   = flag.Int("workers", 4, "并发处理的文件数（1..16）")
		codeArg   = flag.String("codes", "", "只迁移指定代码，逗号分隔，如 sh600000,sz000001")
		policyArg = flag.String("quarantine-policy", "", "本地隔离策略 JSON：明确异常行及经源哈希锁定的已核实坏日")
		resumeLog = flag.String("resume-log", "", "成功 JSONL 回执；同一目标且源哈希不变时跳过已完成文件")
		readLimit = flag.Duration("read-timeout", 5*time.Minute, "单条 SQL 的读超时；大批量写入遇到服务端刷盘变慢时需要放宽")
	)
	flag.Parse()
	if *fromYear != 0 && (*fromYear < 1990 || *fromYear > 2100) {
		return fmt.Errorf("start-year 应为 1990..2100 或 0")
	}
	policy, err := loadSourcePolicy(*policyArg)
	if err != nil {
		return err
	}
	if *workers < 1 || *workers > 16 {
		return fmt.Errorf("migrate-legacy-minute: workers 应在 1..16，当前为 %d", *workers)
	}

	dir := strings.TrimSpace(*dirArg)
	if dir == "" {
		dir = filepath.Join(cfg.GetString("pull.database", extend.DefaultDatabaseDir), extend.DirMinute)
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return err
	}

	dsn := strings.TrimSpace(*dsnArg)
	if *stdinDSN {
		if dsn != "" {
			return fmt.Errorf("-dsn 与 -config-stdin 不能同时使用")
		}
		if err := json.NewDecoder(os.Stdin).Decode(&dsn); err != nil {
			return fmt.Errorf("不能读取私有目标配置")
		}
	}
	if dsn == "" {
		dsn, err = common.MySQLDSN()
		if err != nil {
			return err
		}
	}
	dsn, err = withReadTimeout(dsn, *readLimit)
	if err != nil {
		return err
	}

	jobs, unparsed, err := legacyMinuteJobs(dir, *codeArg, *fromYear)
	if err != nil {
		return err
	}
	if len(jobs) == 0 {
		return fmt.Errorf("migrate-legacy-minute: %s 下没有可迁移的分钟库", dir)
	}

	dst, err := marketstore.NewMySQLImportStore(dsn)
	if err != nil {
		return err
	}
	defer dst.Close()
	keys, err := openMinuteKeys(dsn, *workers)
	if err != nil {
		return err
	}
	defer keys.Close()
	completed := map[string]receipt{}
	if *resumeLog != "" {
		f, err := os.Open(*resumeLog)
		if err != nil {
			return err
		}
		defer f.Close()
		dec := json.NewDecoder(f)
		for {
			var r receipt
			err := dec.Decode(&r)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return err
			}
			if r.File != "" && r.Verified && r.Error == "" && r.Target == dst.Identity() {
				completed[r.File] = r
			}
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	start := time.Now()
	encoder := json.NewEncoder(os.Stdout)
	workersCount := *workers
	if workersCount > len(jobs) {
		workersCount = len(jobs)
	}
	jobsCh := make(chan job)
	results := make(chan result)
	var wg sync.WaitGroup
	for i := 0; i < workersCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobsCh {
				r := result{job: j}
				if err := ctx.Err(); err != nil {
					r.err = err
				} else {
					r.receipt, r.err = migrateLegacyMinute(dst, keys, j, completed[j.file], policy)
				}
				results <- r
			}
		}()
	}
	go func() {
		defer close(jobsCh)
		for _, j := range jobs {
			select {
			case jobsCh <- j:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	totalRows, skipped, invalid, done := 0, 0, 0, 0
	var firstErr error
	for r := range results {
		done++
		totalRows += r.Rows
		if r.Skipped {
			skipped++
		}
		if r.Invalid {
			invalid++
		}
		if r.err != nil && firstErr == nil {
			firstErr = fmt.Errorf("%s-%d: %w", r.job.code.Key(), r.job.year, r.err)
			stop()
		}
		r.AtUTC, r.Seconds = time.Now().UTC(), time.Since(start).Seconds()
		r.File, r.Code, r.Year, r.Target = r.job.file, r.job.code.Key(), r.job.year, dst.Identity()
		r.Done, r.Total = done, len(jobs)
		if r.err != nil {
			r.Error = r.err.Error()
		}
		if err := encoder.Encode(r.receipt); err != nil {
			stop()
			return err
		}
		if done%100 == 0 || r.err != nil {
			fmt.Fprintf(os.Stderr, "files=%d/%d added=%d skipped=%d invalid=%d elapsed=%s\n", done, len(jobs), totalRows, skipped, invalid, time.Since(start).Round(time.Second))
		}
	}
	if firstErr == nil {
		firstErr = ctx.Err()
	}

	if err := encoder.Encode(map[string]any{
		"summary": true, "source": dir, "target": dst.Identity(),
		"files": len(jobs), "minuteRows": totalRows, "skipped": skipped,
		"invalid": invalid, "unparsed": unparsed, "seconds": time.Since(start).Seconds(),
	}); err != nil {
		return err
	}
	if firstErr != nil {
		fmt.Fprintf(os.Stderr, "迁移失败: files=%d rows=%d skipped=%d invalid=%d elapsed=%s\n",
			len(jobs), totalRows, skipped, invalid, time.Since(start).Round(time.Second))
		return firstErr
	}
	fmt.Fprintf(os.Stderr, "迁移完成: files=%d rows=%d skipped=%d invalid=%d elapsed=%s\n",
		len(jobs), totalRows, skipped, invalid, time.Since(start).Round(time.Second))
	return nil
}

// withReadTimeout 放宽驱动的读超时；大批量写入在服务端刷盘变慢时可能超过默认 30s。
func withReadTimeout(dsn string, limit time.Duration) (string, error) {
	if limit <= 0 {
		return dsn, nil
	}
	c, err := mysql.ParseDSN(dsn)
	if err != nil {
		return "", fmt.Errorf("解析目标 DSN: %w", err)
	}
	c.ReadTimeout = limit
	return c.FormatDSN(), nil
}

func legacyMinuteJobs(dir, filter string, fromYear int) ([]job, int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, err
	}
	want := map[string]bool{}
	for _, s := range strings.Split(filter, ",") {
		if s = strings.TrimSpace(s); s != "" {
			want[s] = true
		}
	}
	jobs := []job{}
	unparsed := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, 0, err
		}
		for _, file := range files {
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".db") {
				continue
			}
			name := strings.TrimSuffix(file.Name(), ".db")
			code, year, ok := splitLegacyMinuteName(name)
			if !ok {
				unparsed++
				continue
			}
			if fromYear != 0 && year < fromYear {
				continue
			}
			if len(want) > 0 && !want[code] {
				continue
			}
			c, err := pull.ParseCode(code)
			if err != nil {
				unparsed++
				continue
			}
			jobs = append(jobs, job{file: filepath.Join(dir, entry.Name(), file.Name()), code: c, year: year})
		}
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].year != jobs[j].year {
			return jobs[i].year < jobs[j].year
		}
		return jobs[i].code.Key() < jobs[j].code.Key()
	})
	return jobs, unparsed, nil
}

// splitLegacyMinuteName 拆分 `<code>-<year>`；股票代码本身可能带交易所前缀，年份在最后一个 '-' 之后。
func splitLegacyMinuteName(name string) (string, int, bool) {
	idx := strings.LastIndex(name, "-")
	if idx <= 0 {
		return "", 0, false
	}
	year, err := strconv.Atoi(name[idx+1:])
	if err != nil || year < 1990 || year > 2100 {
		return "", 0, false
	}
	return name[:idx], year, true
}
