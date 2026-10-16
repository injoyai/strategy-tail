package buy

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"testing"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	f "github.com/injoyai/strategy-tail/strategies/factor"
)

// 自定义参数的信号在迁移形态计算前冻结，不随实现重新生成。
func TestMACDShapeMigrationBaseline(t *testing.T) {
	raw, err := os.ReadFile("testdata/macd_shape_baseline.json")
	if err != nil {
		t.Fatal(err)
	}
	var baseline map[string]string
	if err := json.Unmarshal(raw, &baseline); err != nil {
		t.Fatal(err)
	}
	for _, window := range []int{1, 4, 40, 400} {
		for id, buyer := range map[string]core.Buyer{
			"trough":                MACD反转{Fast: 3, Slow: 7, Signal: 3, MinLookback: window, MaxLookback: window + 3},
			"trough_diff":           MACD反转{Fast: 2, Slow: 5, Signal: 2, MinLookback: window, MaxLookback: window - 1, MinDiff: 0.001},
			"shrinking":             MACD负柱缩短{Fast: 3, Slow: 7, Signal: 3, MinDays: window, MaxDays: window + 2, Lookback: 4},
			"shrinking_unbounded":   MACD负柱缩短{Fast: 2, Slow: 5, Signal: 2, MinDays: window, MaxDays: window - 1},
			"smooth_zero":           MACD顺滑{Fast: 3, Slow: 7, Signal: 3, Smooth: 1, Days: window, MaxReversals: 0},
			"smooth_two":            MACD顺滑{Fast: 2, Slow: 5, Signal: 2, Smooth: 3, Days: window, MaxReversals: 2},
			"smooth_legacy":         MACD顺滑{Fast: 3, Slow: 7, Signal: 3, Smooth: -1, Days: window, MaxReversals: 1},
			"max_ratio":             MACD平滑{Fast: 3, Slow: 7, Signal: 3, Days: window, MaxRatio: 0.5},
			"max_ratio_negative":    MACD平滑{Fast: 3, Slow: 7, Signal: 3, Days: window, MaxRatio: -0.5},
			"smooth_negative_limit": MACD顺滑{Fast: 3, Slow: 7, Signal: 3, Smooth: 1, Days: window, MaxReversals: -1},
		} {
			key := fmt.Sprintf("%s_%d", id, window)
			t.Run(key, func(t *testing.T) {
				hash := sha256.New()
				for _, flat := range []bool{false, true} {
					bars := macdShapeBaselineBars(flat)
					for end := 0; end <= len(bars); end++ {
						value := byte(0)
						if buyer.Buy("sh600000", bars[:end]) {
							value = 1
						}
						hash.Write([]byte{value})
					}
				}
				got := fmt.Sprintf("%x", hash.Sum(nil))
				if got != baseline[key] {
					t.Errorf("BASELINE %s %s; want %s", key, got, baseline[key])
				}
			})
		}
	}
}

func macdShapeBaselineBars(flat bool) extend.Klines {
	rng := rand.New(rand.NewSource(27))
	prices := make([]float64, 320)
	price := 10.0
	for i := range prices {
		if !flat {
			price += float64(rng.Intn(1601)-800) / 1000
			if price < 3 {
				price = 3
			}
		}
		prices[i] = price
	}
	return makeKlinesFromCloses(prices)
}

func TestMACDShapeFactorConditions(t *testing.T) {
	for name, pair := range map[string][2]core.Buyer{
		"shrinking":      {MACD负柱缩短{MinDays: 1, MaxDays: 3}, A因子过滤{Factor: f.Build("macd_negative_shrinking_streak", 1), Min: 1, Max: 3}},
		"reversals_zero": {MACD顺滑{Days: 4}, A因子过滤{Factor: f.Build("macd_same_sign_reversals", 4), Min: 0, Max: 0}},
		"reversals_two":  {MACD顺滑{Days: 10, MaxReversals: 2}, A因子过滤{Factor: f.Build("macd_same_sign_reversals", 10), Min: 0, Max: 2}},
		"max_change":     {MACD平滑{Days: 5, MaxRatio: 0.5}, A因子过滤{Factor: f.Build("macd_same_sign_max_change", 5), Min: 0, Max: 0.5}},
		"reversal": {MACD反转{}, And{
			A因子过滤{Factor: f.Build("macd_trough_position", 20), Min: 0, Max: 0},
			A因子过滤{Factor: f.Build("macd_rising_streak", 1), Min: 1, Max: 1e9},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			for _, flat := range []bool{false, true} {
				bars := macdShapeBaselineBars(flat)
				for end := 0; end <= len(bars); end++ {
					if old, new := pair[0].Buy("", bars[:end]), pair[1].Buy("", bars[:end]); old != new {
						t.Fatalf("prefix %d: legacy %v, factor %v", end, old, new)
					}
				}
			}
		})
	}
}
