package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/injoyai/conv/cfg"
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/researchdata"
	"github.com/injoyai/strategy-tail/researchdata/eastmoney"
)

type syncResult struct {
	code    string
	records []researchdata.Record
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
		codesArg = flag.String("codes", "", "逗号分隔股票代码，如 sh600519,sz000001")
		all      = flag.Bool("all", false, "同步当前本地股票列表中的全部股票")
		startArg = flag.String("start", "2010-01-01", "开始交易日 YYYY-MM-DD")
		endArg   = flag.String("end", time.Now().Format(time.DateOnly), "结束交易日 YYYY-MM-DD")
		dbArg    = flag.String("db", "", "估值 SQLite 路径；默认读取 research.valuation.database")
		workers  = flag.Int("workers", 4, "并发请求数")
	)
	flag.Parse()
	if (*codesArg == "") == !*all {
		return fmt.Errorf("valuation-sync: 必须且只能指定 -codes 或 -all 其中一个")
	}
	if *workers < 1 || *workers > 32 {
		return fmt.Errorf("valuation-sync: workers 应在 1..32，当前为 %d", *workers)
	}
	provider := eastmoney.New()
	loc := provider.Location
	start, err := time.ParseInLocation(time.DateOnly, *startArg, loc)
	if err != nil {
		return fmt.Errorf("valuation-sync: start 无效: %w", err)
	}
	end, err := time.ParseInLocation(time.DateOnly, *endArg, loc)
	if err != nil {
		return fmt.Errorf("valuation-sync: end 无效: %w", err)
	}
	end = time.Date(end.Year(), end.Month(), end.Day(), 23, 59, 59, 0, loc)
	if end.Before(start) {
		return fmt.Errorf("valuation-sync: end 早于 start")
	}
	if end.After(time.Now().In(loc)) {
		end = time.Now().In(loc)
	}

	codes, err := resolveCodes(*codesArg, *all)
	if err != nil {
		return err
	}
	dbPath := strings.TrimSpace(*dbArg)
	if dbPath == "" {
		dbPath = cfg.GetString("research.valuation.database", "data/research/valuation.db")
	}
	dbPath = common.ResolveRuntimePath(dbPath)
	store, err := researchdata.OpenSQLiteStore(dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	for _, spec := range provider.Catalog() {
		if err := store.RegisterDataset(spec); err != nil {
			return err
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	jobs := make(chan string)
	results := make(chan syncResult)
	var wg sync.WaitGroup
	for range *workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for code := range jobs {
				records, err := provider.Fetch(ctx, researchdata.Query{
					Dataset: eastmoney.DatasetValuationDaily,
					Codes:   []string{code},
					Start:   start,
					End:     end,
					AsOf:    end,
				})
				results <- syncResult{code: code, records: records, err: err}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, code := range codes {
			select {
			case jobs <- code:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	completed, rows := 0, 0
	var failures []string
	for result := range results {
		completed++
		if result.err == nil {
			result.err = store.Ingest(result.records)
		}
		if result.err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", result.code, result.err))
		} else {
			rows += len(result.records)
		}
		if completed%100 == 0 || completed == len(codes) {
			fmt.Printf("估值历史同步 %d/%d，已写入 %d 行，失败 %d\n", completed, len(codes), rows, len(failures))
		}
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("valuation-sync: 已取消（完成 %d/%d）", completed, len(codes))
	}
	if len(failures) > 0 {
		limit := len(failures)
		if limit > 10 {
			limit = 10
		}
		return fmt.Errorf("valuation-sync: %d 只股票失败（前 %d 条）:\n%s", len(failures), limit, strings.Join(failures[:limit], "\n"))
	}
	fmt.Printf("估值历史同步完成：%d 只股票，%d 行，数据库 %s\n", completed, rows, dbPath)
	return nil
}

func resolveCodes(raw string, all bool) ([]string, error) {
	if all {
		if err := common.Initialize(); err != nil {
			return nil, err
		}
		codes := append([]string(nil), common.GetAllCodes()...)
		sort.Strings(codes)
		return codes, nil
	}
	seen := make(map[string]struct{})
	var codes []string
	for _, rawCode := range strings.Split(raw, ",") {
		code, err := canonicalCode(rawCode)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		codes = append(codes, code)
	}
	if len(codes) == 0 {
		return nil, fmt.Errorf("valuation-sync: codes 为空")
	}
	sort.Strings(codes)
	return codes, nil
}

func canonicalCode(raw string) (string, error) {
	code := strings.ToLower(strings.TrimSpace(raw))
	if len(code) == 8 && (strings.HasPrefix(code, "sh") || strings.HasPrefix(code, "sz") || strings.HasPrefix(code, "bj")) {
		return code, nil
	}
	if len(code) == 9 && code[6] == '.' {
		suffix := code[7:]
		if suffix == "sh" || suffix == "sz" || suffix == "bj" {
			return suffix + code[:6], nil
		}
	}
	if len(code) == 6 {
		switch code[0] {
		case '6':
			return "sh" + code, nil
		case '0', '3':
			return "sz" + code, nil
		case '4', '8', '9':
			return "bj" + code, nil
		}
	}
	return "", fmt.Errorf("valuation-sync: 股票代码无效 %q", raw)
}
