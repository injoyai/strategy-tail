package lab

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/internal/lab/builder"
	"github.com/injoyai/strategy-tail/internal/researchrun"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// BenchmarkReplaySnapshots 测量完整逐日采集与快照写盘，不依赖真实行情库。
func BenchmarkReplaySnapshots(b *testing.B) {
	for _, tc := range []struct {
		name                             string
		codes, days, positions, variants int
	}{
		{"five_years", 300, 1250, 5, 3},
		{"position_limit", 1000, 60, 1000, 1},
	} {
		b.Run(tc.name, func(b *testing.B) {
			dates := make([]string, 0, tc.days)
			for d := time.Date(2020, 1, 2, 0, 0, 0, 0, time.Local); len(dates) < tc.days; d = d.AddDate(0, 0, 1) {
				if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
					dates = append(dates, d.Format(time.DateOnly))
				}
			}
			prices := make([]float64, tc.days)
			for i := range prices {
				prices[i] = 10 + float64(i%5)/10
			}
			bars := accountBars(dates, prices...)
			data := map[string]extend.Klines{"sh000001": bars}
			for i := 0; i < tc.codes; i++ {
				data[fmt.Sprintf("sh%06d", 600000+i)] = bars
			}
			cfg := accountTestConfig(data, accountBuyer{}, accountNeverSell{})
			cfg.Years = nil
			for y := 2020; y <= bars[len(bars)-1].Time.Year(); y++ {
				cfg.Years = append(cfg.Years, y)
			}
			cfg.Workers = 4
			account := AccountConfig{InitialCapital: float64(tc.positions) * 20000, MaxPositions: tc.positions, OrderBudget: 20000}
			b.ReportAllocs()
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				contexts := make([]*builder.RecordContext, tc.variants)
				cfg.Variants = make([]researchrun.Variant, tc.variants)
				for i := range contexts {
					rc := builder.NewRecordContext()
					contexts[i] = rc
					cfg.Variants[i] = researchrun.Variant{Name: fmt.Sprintf("v%d", i), Buyer: rc.WrapBuyerTree(accountBuyer{}, "buy", builder.RoleBuy, true), Seller: rc.WrapSellerTree(accountNeverSell{}, "sell", builder.RoleSell, true)}
				}
				_, _, replay, _, loaded, err := runAccountBacktest(context.Background(), cfg, account, contexts...)
				if err != nil {
					b.Fatal(err)
				}
				if len(replay[0].Days) != tc.days {
					b.Fatal("逐日帧缺失")
				}
				dir := b.TempDir()
				if err := saveReplayFiles(dir, replay, loaded); err != nil {
					b.Fatal(err)
				}
				var bytes int64
				if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if entry.IsDir() {
						return nil
					}
					info, err := entry.Info()
					if err != nil {
						return err
					}
					bytes += info.Size()
					return nil
				}); err != nil {
					b.Fatal(err)
				}
				b.ReportMetric(float64(bytes), "snapshot-B")
			}
		})
	}
}
