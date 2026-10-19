// kline-compress migrates minute SQLite files one at a time, without a full copy.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
)

func main() {
	dir := flag.String("dir", filepath.Join("data", "database"), "行情数据库根目录")
	workers := flag.Int("workers", 4, "并发转换库数（1～8）")
	flag.Parse()
	root, err := filepath.Abs(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoder := json.NewEncoder(os.Stdout)
	start := time.Now()
	done, changed, failed := 0, 0, 0
	var before, after int64
	var records int
	err = (&extend.PullKline{Config: extend.PullKlineConfig{Dir: root}}).CompressMinutes(*workers, func(path string, r extend.MinuteCompression, err error) {
		done++
		before += r.BeforeBytes
		after += r.AfterBytes
		if r.Changed {
			changed++
			records += r.Records
		}
		entry := map[string]any{"path": path, "changed": r.Changed, "records": r.Records, "before": r.BeforeBytes, "after": r.AfterBytes, "sha256": r.SHA256}
		if err != nil {
			failed++
			entry["error"] = err.Error()
		}
		if er := encoder.Encode(entry); er != nil {
			fmt.Fprintln(os.Stderr, er)
			os.Exit(1)
		}
		if done%100 == 0 {
			fmt.Fprintf(os.Stderr, "processed=%d changed=%d failed=%d saved=%.2f GiB elapsed=%s\n", done, changed, failed, float64(before-after)/(1<<30), time.Since(start).Round(time.Second))
		}
	})
	if er := encoder.Encode(map[string]any{"summary": true, "processed": done, "changed": changed, "failed": failed, "records": records, "before": before, "after": after, "saved": before - after, "seconds": time.Since(start).Seconds()}); er != nil {
		fmt.Fprintln(os.Stderr, er)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
