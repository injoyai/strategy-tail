// 浏览器回归只读合成夹具，服务根目录隔离到临时目录，绝不启动行情更新。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/injoyai/strategy-tail/internal/lab"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	root, err := os.MkdirTemp("", "strategy-tail-browser-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	if err := os.Chdir(root); err != nil {
		return err
	}
	rep := &lab.Report{Config: lab.RunConfig{ScriptName: "browser-fixture", StartYear: 2025, EndYear: 2025},
		StartedAt: "2026-10-20T14:00:00+08:00", FinishedAt: "2026-10-20T15:00:00+08:00", Source: "script",
		Variants: []lab.VariantReport{{Name: "策略A", Stats: lab.TradeStatsJSON{Total: 2505, Win: 1504, Loss: 1000, WinRate: 60, AvgProfit: 2.52}}, {Name: "策略B"}}}
	for i := range 2505 {
		rep.Variants[0].Trades = append(rep.Variants[0].Trades, lab.TradeJSON{Code: fmt.Sprintf("sh%06d", 600000+i),
			BuyTime: "2025-01-02 15:00:00", SellTime: "2025-01-03 09:31:00", BuyPrice: 10, SellPrice: 11,
			Quantity: 100, Rate: float64(i)/100 - 10, Profit: float64(i) - 1000, HoldingDays: 1, Virtual: i == 2504})
	}
	rep.Variants[1].Trades = []lab.TradeJSON{{Code: "sz000001", BuyTime: "2025-01-06 15:00:00", SellTime: "2025-01-07 09:31:00",
		BuyPrice: 10, SellPrice: 9, Quantity: 100, Profit: -100, Rate: -10, HoldingDays: 1}}
	for _, report := range []*lab.Report{rep, {Config: rep.Config, FinishedAt: "2026-10-19T15:00:00+08:00", Variants: []lab.VariantReport{}}} {
		dir := filepath.Join("output", "trades", report.RunID())
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		data, err := json.Marshal(report)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "report.json"), data, 0644); err != nil {
			return err
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:18765")
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv := &http.Server{Handler: lab.NewServer().Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(closeCtx)
	}()
	if err := srv.Serve(ln); err != http.ErrServerClosed {
		return err
	}
	return nil
}
