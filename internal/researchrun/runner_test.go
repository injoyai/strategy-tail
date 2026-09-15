package researchrun

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

type alwaysBuyer struct{}

func (alwaysBuyer) Name() string                   { return "always" }
func (alwaysBuyer) Buy(string, extend.Klines) bool { return true }

type nextDaySeller struct{}

func (nextDaySeller) Name() string { return "next day" }
func (nextDaySeller) Sell(_ string, dks extend.Klines, buy core.Buy) bool {
	return len(dks) > 0 && dks[len(dks)-1].Time.After(buy.Time)
}

func TestRunReportsCoverageAndPreservesVariantOrder(t *testing.T) {
	providerErr := errors.New("missing database")
	day := func(code string, _, _ time.Time) (extend.Klines, error) {
		if code == "bad" {
			return nil, providerErr
		}
		return testKlines(), nil
	}
	progress := make([]Progress, 0, 2)
	report, err := Run(context.Background(), Config{
		Codes:         []string{"good", "bad"},
		Years:         []int{2024},
		Variants:      []Variant{{Name: "first", Buyer: alwaysBuyer{}}, {Name: "second", Buyer: alwaysBuyer{}}},
		DefaultSeller: nextDaySeller{},
		Position:      core.DefaultPositionConfig(),
		Workers:       2,
		DataMode:      DailyClose,
		GetDayKlines:  day,
		OnCodeDone:    func(p Progress) { progress = append(progress, p) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Coverage.Requested != 2 || report.Coverage.Completed != 1 || report.Coverage.Skipped != 1 {
		t.Fatalf("unexpected coverage: %+v", report.Coverage)
	}
	if len(report.Coverage.Failures) != 1 || report.Coverage.Failures[0].Stage != "day" {
		t.Fatalf("unexpected failures: %+v", report.Coverage.Failures)
	}
	if len(report.Results) != 2 || report.Results[0].Variant.Name != "first" || report.Results[1].Variant.Name != "second" {
		t.Fatalf("variant order changed: %+v", report.Results)
	}
	if len(report.Results[0].Trades) == 0 || len(report.Results[1].Trades) == 0 {
		t.Fatalf("expected trades for both variants: %+v", report.Results)
	}
	if len(progress) != 2 || progress[1].Done != 2 {
		t.Fatalf("unexpected progress: %+v", progress)
	}
}

func TestRunRequiresMinuteProviderInIntradayMode(t *testing.T) {
	_, err := Run(context.Background(), Config{
		Years:         []int{2024},
		Variants:      []Variant{{Buyer: alwaysBuyer{}}},
		DefaultSeller: nextDaySeller{},
		DataMode:      Intraday,
		GetDayKlines:  func(string, time.Time, time.Time) (extend.Klines, error) { return nil, nil },
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestRunHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Run(ctx, Config{
		Codes:         []string{"good"},
		Years:         []int{2024},
		Variants:      []Variant{{Buyer: alwaysBuyer{}}},
		DefaultSeller: nextDaySeller{},
		DataMode:      DailyClose,
		GetDayKlines:  func(string, time.Time, time.Time) (extend.Klines, error) { return testKlines(), nil },
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

func testKlines() extend.Klines {
	base := time.Date(2023, 12, 29, 0, 0, 0, 0, time.Local)
	prices := []float64{10, 10.1, 10.2}
	result := make(extend.Klines, len(prices))
	for i, price := range prices {
		at := base.AddDate(0, 0, i+3)
		result[i] = &extend.Kline{Kline: &protocol.Kline{
			Time: at, Open: protocol.Yuan(price), High: protocol.Yuan(price),
			Low: protocol.Yuan(price), Close: protocol.Yuan(price), Volume: 100,
		}}
	}
	return result
}
