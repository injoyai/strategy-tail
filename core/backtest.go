package core

import (
	"fmt"
	"sync"
	"time"

	"github.com/injoyai/bar"
	"github.com/injoyai/logs"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

// ============================================================================
// 回测引擎
// ============================================================================

// Backtest 专业级回测引擎。
// 集成成本模型、仓位管理，所有卖出条件（含风控）由 Seller 组合实现，确保回测结果接近实盘。
type Backtest struct {
	Buyer
	Seller
	Goroutines   int
	Codes        []string
	Years        []int
	GetDayKlines GetDayKlines
	GetMinKlines GetMinKlines

	//基准,例沪深300
	Benchmark string

	// 成本模型（默认 DefaultCost）
	Cost Cost

	// 仓位管理（默认 DefaultPositionConfig）
	Position PositionConfig
}

// Run 执行多年份回测，打印结果并导出可视化。
func (this Backtest) Run() {

	logs.Info(this.Buyer.Name() + " 买入")
	logs.Info(this.Seller.Name() + " 卖出")
	logs.Infof("成本: 佣金%.5f 印花税%.5f 滑点%.3f元 最低佣金%.1f元",
		this.Cost.CommissionRate, this.Cost.StampDutyRate,
		this.Cost.Slippage.Float64(), this.Cost.MinCommission)
	logs.Infof("仓位: 单票%d笔 全局上限%d股/笔",
		this.Position.MaxPerCode, this.Position.SharesPerLot)

	results := make([]AnalyzeResult, 0, len(this.Years))
	tradeResults := make(map[int][]Trade, len(this.Years))
	for _, year := range this.Years {

		ls, err := this._backtest(this.Codes, year)
		logs.PanicErr(err)
		tradeResults[year] = ls

		// 拉取当年基准日线
		var benchKlines extend.Klines
		if this.Benchmark != "" {
			benchStart := time.Date(year, 1, 1, 0, 0, 0, 0, time.Local)
			benchEnd := time.Date(year, 12, 31, 23, 0, 0, 0, time.Local)
			benchKlines, _ = this.GetDayKlines(this.Benchmark, benchStart, benchEnd)
		}

		result := Analyze(year, ls, this.GetDayKlines, benchKlines, this.Cost, this.Position)
		results = append(results, result)
	}
	PrintAnalyzeResults(results)
	ExportTradeVisualHTML(this.Years, tradeResults, this.GetDayKlines, results)

	// ---- 阶段二：蒙特卡洛模拟（跨年度全部交易）----
	allTrades := make([]Trade, 0)
	for _, year := range this.Years {
		allTrades = append(allTrades, tradeResults[year]...)
	}
	if len(allTrades) > 10 {
		mc := MonteCarlo(allTrades, 1000, 100000)
		logs.Infof("蒙特卡洛模拟(1000次): 中位收益%.1f%% | 95%%置信区间[%.1f%%, %.1f%%] | 盈利概率%.0f%% | 破产概率%.0f%% | 中位最大回撤%.1f%%\n",
			mc.ReturnP50, mc.ReturnP5, mc.ReturnP95, mc.ProbProfit*100, mc.ProbRuin*100, mc.MaxDrawdownP50)
	}

	// ---- 阶段三：前视偏差审计 ----
	if this.GetDayKlines != nil {
		audit := AuditLookAhead(allTrades, this.Cost, func(code string) (extend.Klines, error) {
			return this.GetDayKlines(code, time.Time{}, time.Now())
		})
		if audit.Passed {
			logs.Info("前视偏差审计: 通过 ✓")
		} else {
			logs.Warnf("前视偏差审计: 发现 %d 个问题", len(audit.Issues))
			for _, issue := range audit.Issues {
				logs.Warn("  - " + issue)
			}
		}
	}
}

func (this Backtest) _backtest(codes []string, year int) ([]Trade, error) {

	hisStart := time.Date(year-2, 6, 1, 0, 0, 0, 0, time.Local)
	start := time.Date(year, 1, 1, 0, 0, 0, 0, time.Local)
	end := time.Date(year, 12, 31, 23, 0, 0, 0, time.Local)

	result := []Trade(nil)
	mu := sync.Mutex{}
	b := bar.NewCoroutine(
		len(codes),
		this.Goroutines,
		bar.WithPrefix(fmt.Sprintf("[%d][%s]", year, "xx000000")),
	)
	defer b.Close()
	for _, code := range codes {
		b.Go(func() {
			b.SetPrefix(fmt.Sprintf("[%d][%s]", year, code))

			dks, err := this.GetDayKlines(code, hisStart, end)
			if err != nil {
				b.Logf("[错误] %s", err)
				b.Flush()
				return
			}

			his := []*extend.Kline(nil)
			for i, v := range dks {
				if v.Time.Before(start) {
					his = append(his, v)
				} else {
					dks = dks[i:]
					break
				}
			}

			var mks protocol.Klines
			if this.GetMinKlines != nil {
				mks, err = this.GetMinKlines(code, start, end)
				if err != nil {
					b.Logf("[错误] %s", err)
					b.Flush()
					return
				}
			}

			ts := this.Do(code, his, dks, mks)
			mu.Lock()
			defer mu.Unlock()
			result = append(result, ts...)
		})

	}
	b.Wait()
	return result, nil
}

// Do 对单只股票执行回测。
// 仓位管理（单票上限）、成本模型集成于引擎；所有卖出条件（含风控）由 Seller 组合实现，
// 在分钟级循环中统一求值。返回该股票的所有成交记录。
func (this Backtest) Do(code string, his, dks extend.Klines, mks protocol.Klines) []Trade {

	cost := this.Cost
	pos := this.Position

	// 分钟线按日期分组（跳过Close=0的无效数据，通常是开盘集合竞价前的空记录）
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

	ts := []Trade(nil)
	currentBuys := make([]Buy, 0)

	for i := 0; i < len(dks); i++ {

		today := dks[i]
		_his := joinKlines(his, dks[:i]...)
		ls := joinKlines(_his, today)

		// ---- 1. 买入信号 ----
		if this.Buy(code, ls) {
			// 仓位管理：单票持仓笔数上限（MaxPerCode > 0 时生效，0=不限）
			if pos.MaxPerCode <= 0 || len(currentBuys) < pos.MaxPerCode {
				currentBuys = append(currentBuys, Buy{
					Code:  code,
					Time:  today.Time,
					Price: today.Close,
				})
			}
		}

		if len(currentBuys) == 0 {
			continue
		}

		// ---- 2. 卖出信号（分钟级精度；风控 Seller 在前由 sell.Or 保证优先）----
		todayMinuteKlines, ok := m[today.Time.Format(time.DateOnly)]
		if !ok || len(todayMinuteKlines) == 0 {
			todayMinuteKlines = protocol.Klines{today.Kline}
		}

		remaining := make([]Buy, 0, len(currentBuys))
		for _, currentBuy := range currentBuys {
			if currentBuy.Time.Equal(today.Time) {
				// T+1：买入当天不卖出
				remaining = append(remaining, currentBuy)
				continue
			}
			sold := false
			for ii := range todayMinuteKlines {
				minuteKlines := todayMinuteKlines[:ii+1]
				lastMinuteKline := todayMinuteKlines[ii]
				// 与原版一致：直接修改 today.Kline（today = dks[i] 是指针）
				// 这会影响后续交易日的 _his，是原版行为，不可更改
				today.Kline = minuteKlines.Kline(lastMinuteKline.Time, lastMinuteKline.Open)

				lsSell := joinKlines(_his, today)
				if this.Sell(code, lsSell, currentBuy) {
					ts = append(ts, this.executeSell(code, currentBuy, today.Close, pos, cost, todayMinuteKlines[ii].Time))
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

	// ---- 3. 期末未平仓：按最后收盘价生成虚拟成交 ----
	if len(currentBuys) > 0 && len(dks) > 0 {
		last := dks[len(dks)-1]
		for _, currentBuy := range currentBuys {
			tr := this.executeSell(code, currentBuy, last.Close, pos, cost, last.Time)
			tr.Virtual = true
			ts = append(ts, tr)
		}
	}

	return ts
}

// executeSell 计算并返回一笔卖出交易（含成本）。
func (this Backtest) executeSell(
	code string,
	buy Buy,
	sellRawPrice protocol.Price,
	pos PositionConfig,
	cost Cost,
	sellTime time.Time,
) Trade {
	quantity := pos.SharesPerLot
	if quantity <= 0 {
		quantity = SharesPerLot
	}

	buyExec, buyCost := cost.BuyCost(buy.Price, quantity)
	sellExec, sellIncome := cost.SellIncome(sellRawPrice, quantity)

	// BuyPrice/SellPrice 存含滑点和手续费的成交价（与原版一致）
	// 原版: BuyPrice = buyExecPrice + buyFee; SellPrice = sellExecPrice - sellFee
	buyFee := protocol.Yuan(buyExec.Float64() * cost.CommissionRate)
	sellFee := protocol.Yuan(sellExec.Float64() * (cost.CommissionRate + cost.StampDutyRate))

	return Trade{
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
