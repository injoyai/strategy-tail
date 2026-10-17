package lab

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/lab/builder"
	"github.com/injoyai/strategy-tail/lib/extend"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
	ss "github.com/injoyai/strategy-tail/strategies/sell"
	"github.com/injoyai/tdx/protocol"
)

// 基线来自拆分因子前的组件；逐前缀覆盖预热期、默认参数、零量与平价窗口。
func TestConditionFactorEquivalence(t *testing.T) {
	raw, err := os.ReadFile("testdata/condition_baseline.json")
	if err != nil {
		t.Fatal(err)
	}
	var baseline map[string]string
	if err := json.Unmarshal(raw, &baseline); err != nil {
		t.Fatal(err)
	}
	for _, entry := range builder.Catalog().Components {
		t.Run(entry.ID, func(t *testing.T) {
			hash := sha256.New()
			for _, params := range []map[string]any{nil, defaultParamsAsJSON(t, entry)} {
				var buyer core.Buyer
				var seller core.Seller
				if entry.Role == "buy" {
					buyer, err = (&BuilderNode{Type: "condition", ComponentID: entry.ID, Params: params}).Buyer()
				} else {
					seller, err = (&BuilderNode{Type: "condition", ComponentID: entry.ID, Params: params}).Seller()
				}
				if err != nil {
					t.Fatal(err)
				}
				for _, flat := range []bool{false, true} {
					bars := conditionBaselineBars(flat)
					for _, code := range []string{"sh600000", "sz300001", "bj920001"} {
						for end := 0; end <= len(bars); end++ {
							matched := false
							if buyer != nil {
								matched = buyer.Buy(code, bars[:end])
							} else {
								matched = seller.Sell(code, bars[:end], core.Buy{Time: bars[20].Time, Price: bars[20].Close})
							}
							if matched {
								hash.Write([]byte{1})
							} else {
								hash.Write([]byte{0})
							}
						}
					}
				}
			}
			got := fmt.Sprintf("%x", hash.Sum(nil))
			if got != baseline[entry.ID] {
				t.Errorf("BASELINE %s %s; want %s", entry.ID, got, baseline[entry.ID])
			}
		})
	}
}

func conditionBaselineBars(flat bool) extend.Klines {
	rng := rand.New(rand.NewSource(27))
	start := time.Date(2025, 1, 1, 15, 0, 0, 0, time.UTC)
	var bars extend.Klines
	price := protocol.Price(10000)
	for i := 0; i < 320; i++ {
		open := price
		if !flat {
			price += protocol.Price(rng.Intn(1601) - 800)
			if price < 3000 {
				price = 3000
			}
		}
		high, low := max(open, price)+300, min(open, price)-300
		volume := int64(1000 + rng.Intn(30000))
		if i%40 < 8 {
			volume = 0
		}
		at := start.AddDate(0, 0, i)
		bars = append(bars, &extend.Kline{Unix: at.Unix(), FloatStock: 10000000000,
			Kline: &protocol.Kline{Time: at, Open: open, Close: price, High: high, Low: low, Volume: volume,
				Amount: protocol.Price(float64(volume) * price.Float64() * 1000)}})
	}
	return bars
}

// --- 基线同步前置行为断言（复核报告 No.12 / No.29+No.30）---
//
// 下面两个测试先于基线哈希更新固化新语义的边界行为：哈希只防回归，
// 语义正确性由这里解释。

// linearBars 生成 n 根逐日涨跌 step 元（可为负）的日 K，起点 start，
// 供条件策略边界行为断言使用。
func linearBars(n int, start, step float64) extend.Klines {
	bars := make(extend.Klines, 0, n)
	price := start
	for i := 0; i < n; i++ {
		bars = append(bars, labelKline(labelDay(i), price, price))
		price += step
	}
	return bars
}

// TestSellLongMADownWarmupBoundary 固化 No.12 新语义：预热不足
// （len(dks) < 250+lookback，maUp 无法判断）时保守不卖出，不再把
// “无法判断”取反当成转弱恒卖；预热刚满后，单调下跌触发卖出、
// 单调上涨不卖出。
func TestSellLongMADownWarmupBoundary(t *testing.T) {
	seller := ss.LongMADown{Lookback: 5}
	buy := core.Buy{}
	decline := linearBars(256, 20, -0.5)
	rise := linearBars(256, 20, 0.5)
	for end := 1; end < 250+5; end++ {
		if seller.Sell("sh600000", decline[:end], buy) {
			t.Fatalf("预热不足 end=%d 不应卖出（无法判断转弱≠转弱）", end)
		}
	}
	if !seller.Sell("sh600000", decline[:250+5], buy) {
		t.Fatal("预热刚满且长均线走弱应卖出")
	}
	if !seller.Sell("sh600000", decline, buy) {
		t.Fatal("完整下跌序列应卖出")
	}
	for end := 250 + 5; end <= len(rise); end++ {
		if seller.Sell("sh600000", rise[:end], buy) {
			t.Fatalf("上涨序列 end=%d 不应卖出", end)
		}
	}
}

// TestBuyRecentGainBelowValidityBoundary 固化 No.29/No.30 新语义：近 N 日
// 涨幅在数据不足（<days+1 根）或起始价无效（<=0）时是“无法计算”，
// 不是“涨幅 0”，不得放行买入；仅当涨幅有效且小于阈值才买入。
func TestBuyRecentGainBelowValidityBoundary(t *testing.T) {
	buyer := sb.A近N日涨幅小于{Days: 5, Max: 15}
	gentle := linearBars(6, 10, 0.1) // 近 5 日涨幅 5%
	if !buyer.Buy("sh600000", gentle) {
		t.Fatal("有效涨幅 5% < 15% 应买入")
	}
	if buyer.Buy("sh600000", gentle[:5]) {
		t.Fatal("数据不足（<days+1 根）不应买入")
	}
	bad := linearBars(6, 10, 0.1)
	bad[0] = labelKline(labelDay(0), 0, 0) // 起始价无效
	if buyer.Buy("sh600000", bad) {
		t.Fatal("起始价无效不应被当作涨幅 0 放行买入")
	}
	hot := linearBars(6, 10, 0.6) // 近 5 日涨幅 30%
	if buyer.Buy("sh600000", hot) {
		t.Fatal("涨幅 30% ≥ 15% 不应买入")
	}
}

// defaultParamsAsJSON 组件目录默认参数经 JSON round-trip（模拟前端提交形态）。
func defaultParamsAsJSON(t *testing.T, entry ComponentEntry) map[string]any {
	t.Helper()
	m := map[string]any{}
	for _, def := range entry.Params {
		if def.Default != nil {
			m[def.Key] = def.Default
		}
	}
	buf, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("组件 %s 默认参数序列化失败: %v", entry.ID, err)
	}
	var out map[string]any
	if err := json.Unmarshal(buf, &out); err != nil {
		t.Fatalf("组件 %s 默认参数反序列化失败: %v", entry.ID, err)
	}
	return out
}
