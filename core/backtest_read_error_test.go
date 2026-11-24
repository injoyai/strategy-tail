package core

import (
	"errors"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/v2/protocol"
)

func TestBacktestReadErrorsInvalidateYear(t *testing.T) {
	readErr := errors.New("provider unavailable")
	for _, stage := range []string{"day", "minute"} {
		for _, codes := range [][]string{{"bad"}, {"bad", "good"}} {
			t.Run(stage+codes[len(codes)-1], func(t *testing.T) {
				bt := Backtest{Buyer: dataBuyer{}, Seller: dataSeller{}, Goroutines: 2,
					GetDayKlines: func(code string, _, _ time.Time) (extend.Klines, error) {
						if stage == "day" && code == "bad" {
							return nil, readErr
						}
						return dataBars(time.Date(2024, 1, 2, 15, 0, 0, 0, time.Local), 2), nil
					},
					GetMinKlines: func(code string, _, _ time.Time) (protocol.Klines, error) {
						if stage == "minute" && code == "bad" {
							return nil, readErr
						}
						return nil, nil
					},
				}
				trades, err := bt._backtest(codes, 2024)
				if !errors.Is(err, readErr) || len(trades) != 0 {
					t.Fatalf("trades=%d err=%v", len(trades), err)
				}
				bt.Codes, bt.Years = codes, []int{2024}
				if got := GridSearch(bt, "test", []float64{1}, func(*Backtest, float64) {}); len(got) != 0 {
					t.Fatal("failed year contributed grid result")
				}
				if got := WalkForward(bt, []int{2024}, []int{2025}); len(got) != 0 {
					t.Fatal("failed training contributed walk-forward result")
				}
			})
		}
	}
}

func TestBacktestEmptyDataIsSuccessful(t *testing.T) {
	bt := Backtest{Goroutines: 1, GetDayKlines: func(string, time.Time, time.Time) (extend.Klines, error) { return nil, nil }}
	trades, err := bt._backtest([]string{"empty"}, 2024)
	if err != nil || len(trades) != 0 {
		t.Fatalf("trades=%v err=%v", trades, err)
	}
}

func TestWalkForwardSkipsFailedTestYear(t *testing.T) {
	bt := Backtest{Buyer: dataBuyer{}, Seller: dataSeller{}, Codes: []string{"test"}, Goroutines: 1,
		GetDayKlines: func(_ string, _, end time.Time) (extend.Klines, error) {
			if end.Year() == 2025 {
				return nil, errors.New("test year unavailable")
			}
			return nil, nil
		},
	}
	if got := WalkForward(bt, []int{2024}, []int{2025}); len(got) != 0 {
		t.Fatal("failed test year contributed result")
	}
}
