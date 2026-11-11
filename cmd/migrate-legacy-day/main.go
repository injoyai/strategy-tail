// migrate-legacy-day 把旧 day-kline（Unix 主键、整数毫/手）转换为源时间日线写入 MySQL。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

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
}

type result struct {
	job     job
	rows    int
	skipped bool
	err     error
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var (
		stdinDSN = flag.Bool("config-stdin", false, "从标准输入读取私有 JSON DSN")
		fromYear = flag.Int("start-year", 0, "只导入该年起的日线；0 表示全部")
		dirArg   = flag.String("dir", "", "旧日线库目录；默认 <pull.database>/day-kline，相对路径按当前目录解析")
		dsnArg   = flag.String("dsn", "", "目标 MySQL DSN；默认读取配置 pull.mysql，建议用 env 注入密码")
		workers  = flag.Int("workers", 4, "并发处理的代码数（1..16）")
		codeArg  = flag.String("codes", "", "只迁移指定代码，逗号分隔，如 sh600000,sz000001")
	)
	flag.Parse()
	if *fromYear != 0 && (*fromYear < 1990 || *fromYear > 2100) {
		return fmt.Errorf("start-year 应为 1990..2100 或 0")
	}
	if *workers < 1 || *workers > 16 {
		return fmt.Errorf("migrate-legacy-day: workers 应在 1..16，当前为 %d", *workers)
	}

	dir := strings.TrimSpace(*dirArg)
	if dir == "" {
		dir = filepath.Join(cfg.GetString("pull.database", extend.DefaultDatabaseDir), extend.DirDay)
	}
	dir, err := filepath.Abs(dir)
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

	jobs, unparsed, err := legacyDayJobs(dir, *codeArg)
	if err != nil {
		return err
	}
	if len(jobs) == 0 {
		return fmt.Errorf("migrate-legacy-day: %s 下没有可迁移的日线库", dir)
	}

	dst, err := marketstore.NewMySQLImportStore(dsn)
	if err != nil {
		return err
	}
	defer dst.Close()

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
					r.rows, r.skipped, r.err = migrateLegacyDay(dst, j, *fromYear)
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

	totalRows, skipped, done := 0, 0, 0
	var firstErr error
	for r := range results {
		done++
		totalRows += r.rows
		if r.skipped {
			skipped++
		}
		if r.err != nil && firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", r.job.code.Key(), r.err)
			stop()
		}
		errorText := ""
		if r.err != nil {
			errorText = r.err.Error()
		}
		if err := encoder.Encode(map[string]any{
			"code": r.job.code.Key(), "market": r.job.code.Market.String(), "rows": r.rows,
			"skipped": r.skipped, "done": done, "total": len(jobs),
			"verified": r.err == nil, "error": errorText, "atUTC": time.Now().UTC(),
		}); err != nil {
			stop()
			return err
		}
	}
	if firstErr == nil {
		firstErr = ctx.Err()
	}

	if err := encoder.Encode(map[string]any{
		"summary": true, "source": dir, "target": dst.Identity(),
		"codes": len(jobs), "dayRows": totalRows, "skipped": skipped,
		"unparsed": unparsed, "seconds": time.Since(start).Seconds(),
	}); err != nil {
		return err
	}
	if firstErr != nil {
		return firstErr
	}
	fmt.Fprintf(os.Stderr, "迁移完成: codes=%d day=%d skipped=%d unparsed=%d elapsed=%s\n",
		len(jobs), totalRows, skipped, unparsed, time.Since(start).Round(time.Second))
	return nil
}

func migrateLegacyDay(dst *marketstore.MySQLStore, j job, fromYear int) (int, bool, error) {
	rows, err := extend.ReadLegacyDaySource(j.file)
	if err != nil {
		return 0, false, err
	}
	if fromYear != 0 {
		cutoff := fmt.Sprintf("%04d-01-01", fromYear)
		rows = rows[sort.Search(len(rows), func(i int) bool { return rows[i].Time >= cutoff }):]
	}
	if len(rows) == 0 {
		return 0, true, nil
	}
	before, err := dst.QueryDayRecords(j.code, rows[0].Time, rows[len(rows)-1].Time)
	if err != nil {
		return 0, false, err
	}
	expected := map[string]*marketstore.DayRecord{}
	for _, k := range before {
		expected[k.Time] = k
	}
	missing := make([]*marketstore.DayRecord, 0)
	for _, k := range rows {
		if expected[k.Time] == nil {
			expected[k.Time] = k
			missing = append(missing, k)
		}
	}
	if len(missing) == 0 {
		return 0, true, nil
	}
	n, err := dst.ImportDayRecords(j.code, missing)
	if err != nil {
		return 0, false, err
	}
	after, err := dst.QueryDayRecords(j.code, rows[0].Time, rows[len(rows)-1].Time)
	if err != nil {
		return n, false, err
	}
	if len(after) != len(expected) {
		return n, false, fmt.Errorf("日线读回数量不一致")
	}
	for _, actual := range after {
		want := expected[actual.Time]
		if want == nil || !reflect.DeepEqual(want, actual) {
			return n, false, fmt.Errorf("日线读回字段不一致：%s", actual.Time)
		}
	}
	return n, n == 0, nil
}

func legacyDayJobs(dir, filter string) ([]job, int, error) {
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
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".db") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".db")
		if len(want) > 0 && !want[name] {
			continue
		}
		code, err := pull.ParseCode(name)
		if err != nil {
			unparsed++
			continue
		}
		jobs = append(jobs, job{file: filepath.Join(dir, entry.Name()), code: code})
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].code.Key() < jobs[j].code.Key() })
	return jobs, unparsed, nil
}
