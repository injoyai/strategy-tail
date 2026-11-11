// migrate-kline 把本地源时间 SQLite 行情复制到 MySQL，按代码/年份幂等写入。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/injoyai/conv/cfg"
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/internal/marketstore"
	"github.com/injoyai/strategy-tail/lib/extend"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var (
		dirArg  = flag.String("dir", "", "SQLite 源时间行情根目录；默认 <pull.database>/source；可显式传入 v1 或 v2 目录，相对路径按当前目录解析")
		dsnArg  = flag.String("dsn", "", "目标 MySQL DSN；默认读取配置 pull.mysql，建议用 env 注入密码")
		workers = flag.Int("workers", 4, "并发处理的代码/年份数（1..16）")
	)
	flag.Parse()
	if *workers < 1 || *workers > 16 {
		return fmt.Errorf("migrate-kline: workers 应在 1..16，当前为 %d", *workers)
	}

	dir := strings.TrimSpace(*dirArg)
	if dir == "" {
		dir = filepath.Join(cfg.GetString("pull.database", extend.DefaultDatabaseDir), "source")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}

	dsn := strings.TrimSpace(*dsnArg)
	if dsn == "" {
		dsn, err = common.MySQLDSN()
		if err != nil {
			return err
		}
	}

	src, err := marketstore.OpenSQLiteSource(dir)
	if err != nil {
		return err
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
	stats, err := marketstore.MigrateSQLite(ctx, src, dst, marketstore.MigrateOptions{
		Workers: *workers,
		OnProgress: func(p marketstore.MigrateProgress) {
			_ = encoder.Encode(map[string]any{
				"market": p.Market, "code": p.Code, "year": p.Year,
				"rows": p.Rows, "skipped": p.Skipped, "done": p.Done, "total": p.Total,
			})
		},
	})
	if er := encoder.Encode(map[string]any{
		"summary": true, "source": src.Identity(), "target": dst.Identity(),
		"codes": stats.Codes, "dayRows": stats.DayRows, "minuteRows": stats.MinuteRows,
		"coverages": stats.Coverages, "skipped": stats.Skipped,
		"seconds": time.Since(start).Seconds(),
	}); er != nil {
		return er
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "迁移完成: codes=%d day=%d minute=%d coverage=%d skipped=%d elapsed=%s\n",
		stats.Codes, stats.DayRows, stats.MinuteRows, stats.Coverages, stats.Skipped,
		time.Since(start).Round(time.Second))
	return nil
}
