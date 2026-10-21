package lab

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

func momentumLowVolatilitySpec(t *testing.T) BuilderSpec {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "strategies", "configs", "momentum_low_volatility.json"))
	if err != nil {
		t.Fatal(err)
	}
	var request struct {
		Spec BuilderSpec `json:"spec"`
	}
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatal(err)
	}
	if err := request.Spec.Validate("run"); err != nil {
		t.Fatal(err)
	}
	return request.Spec
}

func momentumLowVolatilityBars(prices []float64) extend.Klines {
	bars := make(extend.Klines, len(prices))
	day := time.Date(2025, 1, 2, 15, 0, 0, 0, time.FixedZone("CST", 8*3600))
	for i, price := range prices {
		if i > 0 {
			day = day.AddDate(0, 0, 1)
			for day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
				day = day.AddDate(0, 0, 1)
			}
		}
		p := protocol.Yuan(price)
		bars[i] = &extend.Kline{Unix: day.Unix(), Kline: &protocol.Kline{
			Time: day, Open: p, High: p, Low: p, Close: p,
			Volume: 10_000_000, Amount: protocol.Yuan(100_000_000),
		}}
	}
	return bars
}

func TestMomentumLowVolatilityEntry(t *testing.T) {
	spec := momentumLowVolatilitySpec(t)
	buyer, err := spec.Buyer()
	if err != nil {
		t.Fatal(err)
	}
	prices := make([]float64, 127)
	for i := range prices {
		prices[i] = 10 + float64(i)*0.04
	}
	bars := momentumLowVolatilityBars(prices)
	before, err := json.Marshal(bars)
	if err != nil {
		t.Fatal(err)
	}
	if !buyer.Buy("sh600000", bars) {
		t.Fatal("平稳上涨且成交额充足，应满足入场条件")
	}
	if buyer.Buy("sh600000", bars[:126]) || buyer.Buy("sh600000", nil) {
		t.Fatal("缺少完整 127 根历史时不得入场")
	}
	for _, node := range spec.Buy.Children {
		if node.Factor == nil {
			continue
		}
		t.Run(node.Factor.Kind, func(t *testing.T) {
			b, err := node.Buyer()
			if err != nil {
				t.Fatal(err)
			}
			rejected := append([]float64(nil), prices...)
			switch node.Factor.Kind {
			case "skip_month_momentum":
				// 索引 105 为跳过近 21 日的窗口终点，收益只有 4%。
				rejected[105] = 10.4
			case "volatility":
				for i := 67; i < len(rejected); i += 2 {
					rejected[i] *= 1.08
				}
			case "ma_bias":
				rejected[126] = 10
			case "amihud_illiquidity":
				illiquid := momentumLowVolatilityBars(prices)
				for _, bar := range illiquid {
					bar.Amount = protocol.Yuan(1_000_000)
				}
				if b.Buy("sh600000", illiquid) || buyer.Buy("sh600000", illiquid) {
					t.Fatal("单位成交额引起的价格波动过大时必须拒绝")
				}
				// 零成交额表示不可用，不得以 0 因子值放行。
				illiquid = momentumLowVolatilityBars(prices)
				illiquid[126].Amount = 0
				if b.Buy("sh600000", illiquid) || buyer.Buy("sh600000", illiquid) {
					t.Fatal("缺少成交额必须拒绝")
				}
				return
			default:
				t.Fatalf("未覆盖的策略因子 %s", node.Factor.Kind)
			}
			if b.Buy("sh600000", momentumLowVolatilityBars(rejected)) {
				t.Fatal("不满足该因子阈值时必须拒绝")
			}
		})
	}
	after, err := json.Marshal(bars)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) || !buyer.Buy("sh600000", bars) {
		t.Fatal("求值必须只读且确定")
	}
}

func TestMomentumLowVolatilityExit(t *testing.T) {
	spec := momentumLowVolatilitySpec(t)
	for _, node := range spec.Risks {
		t.Run(node.ComponentID, func(t *testing.T) {
			seller, err := node.Seller()
			if err != nil {
				t.Fatal(err)
			}
			var prices []float64
			switch node.ComponentID {
			case "risk_fixed_stop":
				prices = []float64{10, 9.3, 9.1}
			case "risk_trailing_stop":
				prices = []float64{10, 12, 10.7, 10.4}
			case "risk_holding_days":
				prices = make([]float64, 21)
				for i := range prices {
					prices[i] = 10
				}
			default:
				t.Fatalf("未覆盖的风控 %s", node.ComponentID)
			}
			bars := momentumLowVolatilityBars(prices)
			position := core.Buy{Code: "sh600000", Time: bars[0].Time, Price: protocol.Yuan(10)}
			if seller.Sell(position.Code, bars[:len(bars)-1], position) || !seller.Sell(position.Code, bars, position) {
				t.Fatal("风控未按阈值触发，请检查百分数转换与交易日计数")
			}
		})
	}
	seller, err := spec.SellSignal.Seller()
	if err != nil {
		t.Fatal(err)
	}
	prices := make([]float64, 60)
	for i := range prices {
		prices[i] = 10
	}
	prices[59] = 9.9
	if seller.Sell("sh600000", momentumLowVolatilityBars(prices), core.Buy{}) {
		t.Fatal("均线下方 1% 不应触发 2% 缓冲退出")
	}
	prices[59] = 9.7
	if !seller.Sell("sh600000", momentumLowVolatilityBars(prices), core.Buy{}) {
		t.Fatal("均线下方约 3% 应触发退出")
	}
}

func TestMomentumLowVolatilityStoreRoundTrip(t *testing.T) {
	spec := momentumLowVolatilitySpec(t)
	store := NewBuilderStore(t.TempDir())
	saved, err := store.Create(spec)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Get(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.Spec, spec) {
		t.Fatal("保存和恢复必须保留因子版本、参数与独立风控")
	}
}
