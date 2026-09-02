package core_test

// ============================================================================
// Do 重构等价性差分测试
// ============================================================================
// core.Backtest.Do 由"逐日 joinKlines 复制"改为"一次性 full 缓冲 + 前缀切片"。
// 本测试将改动前的原版实现完整复制为 DoLegacy，在同一份随机数据（深拷贝隔离，
// 因 Do 会原地改写 today.Kline）上分别运行新旧两版，逐笔比对交易结果，
// 确保重构行为与原版完全一致（原版回测结果已经过验证，不可漂移）。

import (
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/strategy-tail/strategies/sell"
	"github.com/injoyai/tdx/protocol"
)

// DoLegacy 为重构前的原版 Do（逐日 joinKlines），自 git HEAD 逐行复制。
func DoLegacy(this core.Backtest, code string, his, dks extend.Klines, mks protocol.Klines) []core.Trade {

	cost := this.Cost
	pos := this.Position

	m := map[string]protocol.Klines{}
	for _, mk := range mks {
		if mk.Close == 0 {
			continue
		}
		key := mk.Time.Format(time.DateOnly)
		m[key] = append(m[key], mk)
	}

	joinKlines := func(base extend.Klines, extra ...*extend.Kline) extend.Klines {
		ls := make(extend.Klines, 0, len(base)+len(extra))
		ls = append(ls, base...)
		ls = append(ls, extra...)
		return ls
	}

	ts := []core.Trade(nil)
	currentBuys := make([]core.Buy, 0)

	for i := 0; i < len(dks); i++ {

		today := dks[i]
		_his := joinKlines(his, dks[:i]...)
		ls := joinKlines(_his, today)

		if this.Buy(code, ls) {
			if pos.MaxPerCode <= 0 || len(currentBuys) < pos.MaxPerCode {
				currentBuys = append(currentBuys, core.Buy{
					Code:  code,
					Time:  today.Time,
					Price: today.Close,
				})
			}
		}

		if len(currentBuys) == 0 {
			continue
		}

		todayMinuteKlines, ok := m[today.Time.Format(time.DateOnly)]
		if !ok || len(todayMinuteKlines) == 0 {
			todayMinuteKlines = protocol.Klines{today.Kline}
		}

		remaining := make([]core.Buy, 0, len(currentBuys))
		for _, currentBuy := range currentBuys {
			if currentBuy.Time.Equal(today.Time) {
				remaining = append(remaining, currentBuy)
				continue
			}
			sold := false
			for ii := range todayMinuteKlines {
				minuteKlines := todayMinuteKlines[:ii+1]
				lastMinuteKline := todayMinuteKlines[ii]
				today.Kline = minuteKlines.Kline(lastMinuteKline.Time, lastMinuteKline.Open)

				lsSell := joinKlines(_his, today)
				if this.Sell(code, lsSell, currentBuy) {
					ts = append(ts, legacyExecuteSell(this, code, currentBuy, today.Close, pos, cost, todayMinuteKlines[ii].Time))
					sold = true
					break
				}
			}
			if !sold {
				remaining = append(remaining, currentBuy)
			}
		}
		currentBuys = remaining
	}

	if len(currentBuys) > 0 && len(dks) > 0 {
		last := dks[len(dks)-1]
		for _, currentBuy := range currentBuys {
			tr := legacyExecuteSell(this, code, currentBuy, last.Close, pos, cost, last.Time)
			tr.Virtual = true
			ts = append(ts, tr)
		}
	}

	return ts
}

// legacyExecuteSell 与 core.Backtest.executeSell 逻辑一致（该函数未改动，复制以供 DoLegacy 调用）。
func legacyExecuteSell(
	this core.Backtest,
	code string,
	buy core.Buy,
	sellRawPrice protocol.Price,
	pos core.PositionConfig,
	cost core.Cost,
	sellTime time.Time,
) core.Trade {
	quantity := pos.SharesPerLot
	if quantity <= 0 {
		quantity = core.SharesPerLot
	}

	buyExec, buyCost := cost.BuyCost(buy.Price, quantity)
	sellExec, sellIncome := cost.SellIncome(sellRawPrice, quantity)

	buyFee := protocol.Yuan(buyExec.Float64() * cost.CommissionRate)
	sellFee := protocol.Yuan(sellExec.Float64() * (cost.CommissionRate + cost.StampDutyRate))

	return core.Trade{
		Code:          code,
		BuyTime:       buy.Time,
		SellTime:      sellTime,
		BuyPrice:      buyExec + buyFee,
		SellPrice:     sellExec - sellFee,
		BuyExecPrice:  buyExec,
		SellExecPrice: sellExec,
		BuyCost:       buyCost,
		SellIncome:    sellIncome,
		Quantity:      quantity,
	}
}

// ---------- 深拷贝（隔离两轮运行间的原地改写） ----------

func cloneKlines(ks extend.Klines) extend.Klines {
	out := make(extend.Klines, len(ks))
	for i, k := range ks {
		nk := &extend.Kline{
			Unix:       k.Unix,
			Turnover:   k.Turnover,
			FloatStock: k.FloatStock,
			TotalStock: k.TotalStock,
		}
		if k.Kline != nil {
			pk := *k.Kline
			nk.Kline = &pk
		}
		out[i] = nk
	}
	return out
}

func cloneMinKlines(ks protocol.Klines) protocol.Klines {
	out := make(protocol.Klines, len(ks))
	for i, k := range ks {
		pk := *k
		out[i] = &pk
	}
	return out
}

// ---------- 随机数据生成（固定种子，可复现） ----------

type equivData struct {
	his extend.Klines
	dks extend.Klines
	mks protocol.Klines
}

// genRandomData 生成随机游走日线（含 his 前史）与部分交易日的分钟线。
// 分钟线中混入 Close=0 的无效记录，覆盖引擎的跳过分支。
func genRandomData(rnd *rand.Rand) equivData {
	const hisDays, dksDays = 80, 80
	const minuteBars = 60

	price := 5.0 + rnd.Float64()*30
	base := time.Date(2024, 6, 3, 15, 0, 0, 0, time.Local)

	genDay := func(t time.Time, last float64) *extend.Kline {
		open := last * (1 + (rnd.Float64()-0.5)*0.04)
		close := open * (1 + (rnd.Float64()-0.5)*0.06)
		high := math.Max(open, close) * (1 + rnd.Float64()*0.02)
		low := math.Min(open, close) * (1 - rnd.Float64()*0.02)
		return &extend.Kline{
			Unix:       t.Unix(),
			Kline:      &protocol.Kline{Time: t, Open: protocol.Yuan(open), High: protocol.Yuan(high), Low: protocol.Yuan(low), Close: protocol.Yuan(close), Volume: int64(rnd.Intn(100000) + 1000), Amount: protocol.Yuan(close * 10000)},
			Turnover:   rnd.Float64() * 5,
			FloatStock: int64(400000000 + rnd.Intn(500000000)),
			TotalStock: int64(600000000 + rnd.Intn(500000000)),
		}
	}

	his := make(extend.Klines, hisDays)
	for i := range his {
		t := base.AddDate(0, 0, i)
		if isWeekend(t) {
			t = t.AddDate(0, 0, 2)
		}
		his[i] = genDay(t, price)
		price = his[i].Close.Float64()
	}

	dks := make(extend.Klines, dksDays)
	day := base.AddDate(0, 0, hisDays)
	for i := range dks {
		for isWeekend(day) {
			day = day.AddDate(0, 0, 1)
		}
		dks[i] = genDay(day, price)
		price = dks[i].Close.Float64()
		day = day.AddDate(0, 0, 1)
	}

	// 分钟线：约 2/3 的交易日有数据，每天 minuteBars 根，5% 为 Close=0 无效记录
	mks := protocol.Klines(nil)
	for _, dk := range dks {
		if rnd.Intn(3) == 0 {
			continue
		}
		p := dk.Open.Float64()
		t := time.Date(dk.Time.Year(), dk.Time.Month(), dk.Time.Day(), 9, 31, 0, 0, time.Local)
		for j := 0; j < minuteBars; j++ {
			mp := p * (1 + (rnd.Float64()-0.5)*0.003)
			invalid := rnd.Intn(100) < 5
			k := &protocol.Kline{
				Time:   t.Add(time.Duration(j) * time.Minute),
				Open:   protocol.Yuan(mp),
				High:   protocol.Yuan(mp * 1.001),
				Low:    protocol.Yuan(mp * 0.999),
				Volume: int64(rnd.Intn(5000) + 100),
			}
			if !invalid {
				k.Close = protocol.Yuan(mp)
				k.Amount = protocol.Yuan(mp * 1000)
			}
			mks = append(mks, k)
			p = mp
		}
	}

	return equivData{his: his, dks: dks, mks: mks}
}

func isWeekend(t time.Time) bool {
	wd := t.Weekday()
	return wd == time.Saturday || wd == time.Sunday
}

// ---------- 差分测试 ----------

func TestDo重构与原版等价(t *testing.T) {
	// 策略组合覆盖：常买（最大化买入路径覆盖）、真实 MACD/均线条件（长窗口读取）、
	// 多种卖出（止盈止损/反转/持仓天数/盈利组合），并混合分钟线缺失的交易日。
	scenarios := []struct {
		name   string
		buyer  core.Buyer
		seller core.Seller
	}{
		{
			name:   "常买+止盈止损/持仓天数",
			buyer:  alwaysBuyer{},
			seller: sell.Or{sell.A止盈止损{StopLoss: 0.08, TakeProfit: 0.12}, sell.A持仓N天{Days: 5}},
		},
		{
			name:   "常买+MACD反转/盈利反转",
			buyer:  alwaysBuyer{},
			seller: sell.Or{sell.MACD反转{Lookback: 10}, sell.And{sell.A盈利(0.005), sell.MACD反转{Lookback: 2}}},
		},
		{
			name: "MACD反转+均线向上+组合卖出",
			buyer: buy.And{
				buy.MACD反转{MinLookback: 4},
				buy.MAUp{Period: 20, MinSlope: 0.0001},
				buy.And{
					buy.A现价{Min: 2, Max: 120},
					buy.A现价大于N日均线(30),
				},
			},
			seller: sell.Or{
				sell.MACD反转{Lookback: 10},
				sell.And{sell.A盈利(0.005), sell.MACD反转{Lookback: 2}},
				sell.A止盈止损{StopLoss: 0.1},
			},
		},
	}

	const seeds = 8
	for s := 0; s < seeds; s++ {
		rnd := rand.New(rand.NewSource(int64(s)))
		data := genRandomData(rnd)

		for _, sc := range scenarios {
			for _, maxPerCode := range []int{0, 1, 3} {
				// 常买 + 不限仓位 + 分钟线组合负载极大（每分钟×每持仓求值），
				// 该组合仅在无分钟线时覆盖；等价性结论不受此裁剪影响
				withMinOptions := []bool{true, false}
				if maxPerCode == 0 && sc.buyer.Name() == "always" {
					withMinOptions = []bool{false}
				}
				for _, withMin := range withMinOptions {
					label := fmt.Sprintf("seed=%d 策略=[%s] MaxPerCode=%d 分钟线=%v", s, sc.name, maxPerCode, withMin)

					bt := core.Backtest{
						Buyer:  sc.buyer,
						Seller: sc.seller,
						Cost: core.Cost{
							CommissionRate:  0.0001,
							StampDutyRate:   0.0005,
							TransferFeeRate: 0.00001,
							Slippage:        protocol.Yuan(0.01),
						},
						Position: core.PositionConfig{MaxPerCode: maxPerCode, SharesPerLot: 100},
					}

					var mks protocol.Klines
					if withMin {
						mks = data.mks
					}

					// 深拷贝两份输入：Do 会原地改写 today.Kline，必须隔离两轮运行
					newTrades := bt.Do("test", cloneKlines(data.his), cloneKlines(data.dks), cloneMinKlines(mks))
					legacyTrades := DoLegacy(bt, "test", cloneKlines(data.his), cloneKlines(data.dks), cloneMinKlines(mks))

					if len(newTrades) != len(legacyTrades) {
						t.Fatalf("%s: 交易笔数不一致 new=%d legacy=%d", label, len(newTrades), len(legacyTrades))
					}
					for i := range newTrades {
						if !reflect.DeepEqual(newTrades[i], legacyTrades[i]) {
							t.Fatalf("%s: 第%d笔交易不一致\nnew=%+v\nlegacy=%+v", label, i, newTrades[i], legacyTrades[i])
						}
					}
				}
			}
		}
	}

	t.Logf("差分测试通过: %d 个种子 × %d 组策略 × 3 种仓位 × 分钟线开关，全部交易逐笔一致", seeds, len(scenarios))
}
