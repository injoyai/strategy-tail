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
	"github.com/injoyai/strategy-tail/lib/extend"
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
	for _, entry := range builderCatalog() {
		t.Run(entry.ID, func(t *testing.T) {
			hash := sha256.New()
			for _, params := range []map[string]any{nil, defaultParamsAsJSON(t, entry)} {
				var buyer core.Buyer
				var seller core.Seller
				if entry.Role == roleBuy {
					buyer, err = buyFactories[entry.ID](params)
				} else {
					seller, err = sellFactories[entry.ID](params)
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
