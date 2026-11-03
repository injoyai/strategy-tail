package lab

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/injoyai/strategy-tail/core"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/tdx/v2/protocol"
)

// 补充搭建器目录之外的策略，基线在迁移其计算前冻结。
func TestAdditionalConditionFactorEquivalence(t *testing.T) {
	raw, err := os.ReadFile("testdata/factor_migration_baseline_v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var baseline map[string]string
	if err := json.Unmarshal(raw, &baseline); err != nil {
		t.Fatal(err)
	}
	index := conditionBaselineBars(false)
	var rawIndex protocol.Klines
	for _, k := range index {
		rawIndex = append(rawIndex, k.Kline)
	}
	for id, buyer := range map[string]core.Buyer{
		"long_ma":            sb.A长期均线多头{},
		"long_ma_custom":     sb.A长期均线多头{Short: 3, Medium: 7, Long: 12},
		"above_long_ma":      sb.A收盘价站上长期均线{},
		"above_long_custom":  sb.A收盘价站上长期均线{Period: 7, Margin: 0.15},
		"shallow_pullback":   sb.A浅回调至均线{},
		"shallow_custom":     sb.A浅回调至均线{MAPeriod: 3, HighLookback: 7, MinPullback: 1, MaxPullback: 30, MAProximity: 20},
		"stabilize":          sb.A企稳信号{},
		"shrink_stabilize":   sb.A缩量企稳{},
		"year_ma":            sb.A年线向上{},
		"year_ma_custom":     sb.A年线向上{Lookback: 2, MinSlope: 0.0002},
		"tail_pullback":      sb.A阴线收回{MinBodyRatio: 0.3},
		"index_bull":         sb.A指数多头排列{Ks: index},
		"index_bull_custom":  sb.A指数多头排列{Ks: index, Periods: []int{2, 5, 10}},
		"index_above":        sb.A指数站上MA{Ks: index},
		"index_above_custom": sb.A指数站上MA{Ks: index, Period: 3},
		"shanghai_ma":        sb.A上证N日均线向上{Ks: rawIndex},
		"shanghai_custom":    sb.A上证N日均线向上{Ks: rawIndex, Period: 3, Lookback: 2, MinSlope: 0.0002},
		"tdx_volume":         sb.A通达信倍量{},
		"new_high":           sb.A突破N天新高(7),
		"close_high":         sb.A近N天最高价(7),
		"smooth_macd":        sb.MACD顺滑{Fast: 3, Slow: 7, Signal: 3, Smooth: 2, Days: 4, MaxReversals: 1},
	} {
		t.Run(id, func(t *testing.T) {
			hash := sha256.New()
			for _, flat := range []bool{false, true} {
				bars := conditionBaselineBars(flat)
				for end := 0; end <= len(bars); end++ {
					matched := buyer.Buy("sh600000", bars[:end])
					if matched {
						hash.Write([]byte{1})
					} else {
						hash.Write([]byte{0})
					}
				}
			}
			got := fmt.Sprintf("%x", hash.Sum(nil))
			if got != baseline[id] {
				t.Errorf("BASELINE %s %s; want %s", id, got, baseline[id])
			}
		})
	}
}
