package lab

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/lab/builder"
	"github.com/injoyai/strategy-tail/internal/researchrun"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/v2/protocol"
)

// AccountConfig 是随请求及报告冻结的资金约束；OrderBudget 包含买入费用。
type AccountConfig struct {
	InitialCapital float64 `json:"initialCapital"`
	MaxPositions   int     `json:"maxPositions"`
	OrderBudget    float64 `json:"orderBudget"`
}

func (c AccountConfig) Validate() error {
	if math.IsNaN(c.InitialCapital) || math.IsInf(c.InitialCapital, 0) || c.InitialCapital <= 0 || c.InitialCapital > 1e12 {
		return fmt.Errorf("初始资金需为不超过 1 万亿元的正数")
	}
	if c.MaxPositions <= 0 || c.MaxPositions > 1000 {
		return fmt.Errorf("同时持仓数量需为 1～1000 的整数")
	}
	if math.IsNaN(c.OrderBudget) || math.IsInf(c.OrderBudget, 0) || c.OrderBudget <= 0 || c.OrderBudget > 1e12 {
		return fmt.Errorf("每笔买入预算需为不超过 1 万亿元的正数")
	}
	return nil
}

type AccountPoint struct {
	Date     string  `json:"date"`
	Equity   float64 `json:"equity"`
	Cash     float64 `json:"cash"`
	Holdings int     `json:"holdings"`
	Return   float64 `json:"return"` // 百分数，与 TradeJSON.rate 一致。
}

type BenchmarkPoint struct {
	Date   string  `json:"date"`
	Return float64 `json:"return"`
}

type AccountReport struct {
	Points             []AccountPoint `json:"points"`
	MissingMinuteDays  int            `json:"missingMinuteDays"`
	StaleValuationDays int            `json:"staleValuationDays"`
	Execution          string         `json:"execution,omitempty"`
	UnfilledBuys       int            `json:"unfilledBuys,omitempty"`
	PendingSells       int            `json:"pendingSells,omitempty"`
}

const accountExecutionNextPrice = "next_available_bar"

type accountSignal struct {
	at     time.Time
	reason []string
	daily  bool
}

type accountBuySignal struct {
	code string
	accountSignal
}

type accountHolding struct {
	buy        core.Buy
	trade      core.Trade
	mark       float64
	positionID int
	markDate   string
	exit       *accountSignal
}

type accountState struct {
	cash      float64
	holdings  []accountHolding
	trades    []core.Trade
	report    *AccountReport
	replay    *ReplayVariant
	nextPosID int
	entries   []accountBuySignal
}

type accountMinutes struct {
	year int
	days map[string]protocol.Klines
}

// runAccountBacktest 将信号和成交分开执行，账户在每日收盘估值。
// 复用 Buyer/Seller 和 Cost；不会把逐笔结果排序后冒充资金约束回测。
// contexts 按变体顺序提供原因记录上下文（可变参，缺省即不记录）；
// 采集与求值顺序解耦，不影响成交与估值结果（R07/R15）。
// 第三个返回值为逐变体回放数据：未提供记录上下文的变体为 nil。
// 第五个返回值为本次回测已加载的个股日线副本（键为带前缀代码，含前导历史，
// 供保存阶段生成回放个股 K 线快照），仅账户回测路径填充。
func runAccountBacktest(ctx context.Context, cfg researchrun.Config, account AccountConfig, contexts ...*builder.RecordContext) (researchrun.Report, []*AccountReport, []*ReplayVariant, []BenchmarkPoint, map[string]extend.Klines, error) {
	var result researchrun.Report
	if err := account.Validate(); err != nil {
		return result, nil, nil, nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return result, nil, nil, nil, nil, err
	}
	if len(cfg.Years) == 0 || len(cfg.Variants) == 0 || cfg.GetDayKlines == nil {
		return result, nil, nil, nil, nil, fmt.Errorf("账户回测缺少年份、策略或行情源")
	}
	for _, v := range cfg.Variants {
		if v.Buyer == nil || (v.Seller == nil && cfg.DefaultSeller == nil) {
			return result, nil, nil, nil, nil, fmt.Errorf("账户回测缺少买入或卖出规则")
		}
	}
	years := append([]int(nil), cfg.Years...)
	sort.Ints(years)
	start := time.Date(years[0], 1, 1, 0, 0, 0, 0, time.Local)
	end := time.Date(years[len(years)-1], 12, 31, 23, 59, 59, 0, time.Local)
	index, err := cfg.GetDayKlines("sh000001", start, end)
	if err != nil {
		return result, nil, nil, nil, nil, fmt.Errorf("读取上证指数: %w", err)
	}
	if len(index) == 0 {
		return result, nil, nil, nil, nil, fmt.Errorf("回测区间缺少上证指数日线，请先更新行情")
	}
	for _, k := range index {
		if k == nil || k.Kline == nil || !validExecutionPrice(k.Close) {
			return result, nil, nil, nil, nil, fmt.Errorf("上证指数存在无效收盘价")
		}
	}
	index = append(extend.Klines(nil), index...)
	sort.Slice(index, func(i, j int) bool { return index[i].Time.Before(index[j].Time) })
	calendar := make(map[string]bool, len(index))
	benchmark := make([]BenchmarkPoint, 0, len(index))
	for _, k := range index {
		date := k.Time.Format(time.DateOnly)
		if calendar[date] {
			return result, nil, nil, nil, nil, fmt.Errorf("上证指数存在重复交易日: %s", date)
		}
		calendar[date] = true
		benchmark = append(benchmark, BenchmarkPoint{Date: date, Return: (k.Close/index[0].Close - 1) * 100})
	}
	// 代码顺序决定同一收盘时刻多个信号的开盘买入优先级。
	codes := append([]string(nil), cfg.Codes...)
	for i := range codes {
		codes[i] = protocol.AddPrefix(codes[i])
	}
	sort.Strings(codes)
	unique := codes[:0]
	for _, code := range codes {
		if len(unique) == 0 || unique[len(unique)-1] != code {
			unique = append(unique, code)
		}
	}
	codes = unique
	result.Coverage.Requested = len(codes)
	data := map[string]extend.Klines{}
	var mu sync.Mutex
	loadCfg := cfg
	loadCfg.Codes, loadCfg.Years, loadCfg.DataMode = codes, years, researchrun.DailyClose
	if cfg.OnCodeDone != nil {
		cfg.OnCodeDone(researchrun.Progress{Phase: "loading", Total: len(codes)})
	}
	loadCfg.OnCodeDone = func(p researchrun.Progress) {
		if p.Failure != nil {
			result.Coverage.Failures = append(result.Coverage.Failures, *p.Failure)
		}
		if cfg.OnCodeDone != nil {
			p.Phase = "loading"
			cfg.OnCodeDone(p)
		}
	}
	err = researchrun.ForEachCodeData(ctx, loadCfg, func(code string, datas []researchrun.YearData) {
		full := append(extend.Klines(nil), datas[0].His...)
		for _, d := range datas {
			full = append(full, d.Dks...)
		}
		mu.Lock()
		data[code] = full
		mu.Unlock()
	})
	if err != nil {
		return result, nil, nil, nil, nil, err
	}
	for _, code := range codes {
		ks, ok := data[code]
		if !ok {
			continue
		}
		for i, k := range ks {
			if k == nil || k.Kline == nil || !validExecutionPrice(k.Close) || (i > 0 && !k.Time.After(ks[i-1].Time)) {
				return result, nil, nil, nil, nil, fmt.Errorf("%s 日线存在无效价格或日期顺序", code)
			}
			if !k.Time.Before(start) && !calendar[k.Time.Format(time.DateOnly)] {
				return result, nil, nil, nil, nil, fmt.Errorf("上证指数缺少 %s 交易日，请先更新指数行情", k.Time.Format(time.DateOnly))
			}
		}
		result.Coverage.Completed++
	}
	result.Coverage.Skipped = len(codes) - result.Coverage.Completed
	states := make([]accountState, len(cfg.Variants))
	accounts := make([]*AccountReport, len(states))
	replays := make([]*ReplayVariant, len(states))
	result.Results = make([]researchrun.Result, len(states))
	for i, variant := range cfg.Variants {
		accounts[i] = &AccountReport{Points: make([]AccountPoint, 0, len(index)), Execution: accountExecutionNextPrice}
		states[i] = accountState{cash: account.InitialCapital, report: accounts[i]}
		result.Results[i].Variant = variant
		// 提供记录上下文的变体旁路采集回放数据；其余变体保持 nil，路径零开销。
		if i < len(contexts) && contexts[i] != nil {
			replays[i] = &ReplayVariant{
				Version:      replayVersion,
				VariantIndex: i,
				VariantName:  variant.Name,
				Dates:        make([]string, 0, len(index)),
				Days:         make([]ReplayDay, 0, len(index)),
			}
			states[i].replay = replays[i]
		}
	}
	cursors := map[string]int{}
	minutes := map[string]accountMinutes{}
	if cfg.OnCodeDone != nil {
		cfg.OnCodeDone(researchrun.Progress{Phase: "simulating", Total: len(index)})
	}
	for day, k := range index {
		if err := ctx.Err(); err != nil {
			return result, nil, nil, nil, nil, err
		}
		date := k.Time.Format(time.DateOnly)
		prefixes := map[string]extend.Klines{}
		for _, code := range codes {
			ks := data[code]
			j := cursors[code]
			for j < len(ks) && ks[j].Time.Format(time.DateOnly) <= date {
				j++
			}
			cursors[code] = j
			if j > 0 && ks[j-1].Time.Format(time.DateOnly) == date {
				prefixes[code] = ks[:j]
			}
		}
		for i, variant := range cfg.Variants {
			s := &states[i]
			seller := variant.Seller
			if seller == nil {
				seller = cfg.DefaultSeller
			}
			// 隔夜卖出先在开盘撮合，只有已经成交的卖单能为开盘买单释放现金。
			remaining := make([]accountHolding, 0, len(s.holdings))
			for _, holding := range s.holdings {
				ks := prefixes[holding.buy.Code]
				if holding.exit != nil && len(ks) != 0 {
					today := ks[len(ks)-1]
					if canFillStockBar(holding.buy.Code, today.Kline, priorStockClose(ks), today.Open, false) {
						if err := s.sellAt(holding, accountOpenTime(today.Time), today.Open, cfg.Cost); err != nil {
							return result, nil, nil, nil, nil, err
						}
						continue
					}
				}
				remaining = append(remaining, holding)
			}
			s.holdings = remaining
			for _, signal := range s.entries {
				if !s.buyAtOpen(signal, prefixes[signal.code], account, cfg.Cost) {
					s.report.UnfilledBuys++
				}
			}
			s.entries = s.entries[:0]

			remaining = make([]accountHolding, 0, len(s.holdings))
			for _, holding := range s.holdings {
				ks := prefixes[holding.buy.Code]
				if len(ks) == 0 {
					s.report.StaleValuationDays++
					remaining = append(remaining, holding)
					continue
				}
				today := ks[len(ks)-1]
				holding.mark = today.Close
				holding.markDate = date
				if today.Volume <= 0 || holding.buy.Time.Format(time.DateOnly) == date {
					remaining = append(remaining, holding)
					continue
				}
				cached, ok := minutes[holding.buy.Code]
				if !ok || cached.year != k.Time.Year() {
					cached = accountMinutes{year: k.Time.Year(), days: map[string]protocol.Klines{}}
					if cfg.GetMinKlines != nil {
						from := time.Date(k.Time.Year(), 1, 1, 0, 0, 0, 0, time.Local)
						to := time.Date(k.Time.Year(), 12, 31, 23, 59, 59, 0, time.Local)
						mks, err := cfg.GetMinKlines(holding.buy.Code, from, to)
						if err != nil {
							return result, nil, nil, nil, nil, fmt.Errorf("%s 分钟行情: %w", holding.buy.Code, err)
						}
						for _, mk := range mks {
							if mk != nil && validExecutionPrice(mk.Close) {
								d := mk.Time.Format(time.DateOnly)
								cached.days[d] = append(cached.days[d], mk)
							}
						}
						for _, bars := range cached.days {
							bars.Sort()
						}
					}
					minutes[holding.buy.Code] = cached
				}
				bars := cached.days[date]
				if len(bars) == 0 {
					s.report.MissingMinuteDays++
					if holding.exit == nil && seller.Sell(holding.buy.Code, ks, holding.buy) {
						holding.exit = newAccountSignal(accountCloseTime(today.Time), true, s.replay, contexts, i)
					}
					remaining = append(remaining, holding)
					continue
				}
				// Seller 只能看到截至当前分钟的数据；不覆写真实日线收盘价。
				sellKs := append(extend.Klines(nil), ks...)
				current := *today
				sellKs[len(sellKs)-1] = &current
				sold := false
				previous := priorStockClose(ks)
				for m, bar := range bars {
					if err := ctx.Err(); err != nil {
						return result, nil, nil, nil, nil, err
					}
					if holding.exit != nil {
						if bar.Time.After(holding.exit.at) && canFillStockBar(holding.buy.Code, bar, previous, bar.Close, false) {
							if err := s.sellAt(holding, bar.Time, bar.Close, cfg.Cost); err != nil {
								return result, nil, nil, nil, nil, err
							}
							sold = true
							break
						}
						continue
					}
					if bar.Volume <= 0 {
						continue
					}
					current.Kline = bars[:m+1].Kline(bar.Time, previous)
					current.Last = previous
					current.Unix = bar.Time.Unix()
					current.Turnover = 0
					if current.FloatStock > 0 {
						equity := protocol.Equity{Float: current.FloatStock}
						current.Turnover = equity.Turnover(protocol.ToShares(current.Volume))
					}
					if seller.Sell(holding.buy.Code, sellKs, holding.buy) {
						holding.exit = newAccountSignal(bar.Time, false, s.replay, contexts, i)
					}
				}
				if !sold {
					remaining = append(remaining, holding)
				}
			}
			s.holdings = remaining
			if day+1 < len(index) {
				for _, code := range codes {
					ks := prefixes[code]
					if len(ks) != 0 && ks[len(ks)-1].Volume > 0 && s.canSignalEntry(code) && variant.Buyer.Buy(code, ks) {
						signal := newAccountSignal(accountCloseTime(ks[len(ks)-1].Time), false, s.replay, contexts, i)
						s.entries = append(s.entries, accountBuySignal{code: code, accountSignal: *signal})
					}
				}
			}
			equity := s.cash
			for _, h := range s.holdings {
				equity += h.mark * float64(h.trade.Quantity)
			}
			s.report.Points = append(s.report.Points, AccountPoint{Date: date, Equity: equity, Cash: s.cash, Holdings: len(s.holdings), Return: (equity/account.InitialCapital - 1) * 100})
			// 收盘帧点之后立即冻结当日回放帧：持仓为不可变副本，事件为当日全部成交。
			if s.replay != nil {
				marketValue := equity - s.cash
				day := ReplayDay{
					Date:        date,
					Equity:      equity,
					Cash:        s.cash,
					MarketValue: marketValue,
				}
				if n := len(s.report.Points); n >= 2 {
					day.DayPnL = equity - s.report.Points[n-2].Equity
				} else {
					day.DayPnL = equity - account.InitialCapital
				}
				if equity > 0 {
					day.PositionRatio = marketValue / equity * 100
				}
				for _, h := range s.holdings {
					totalCost := h.trade.BuyCost
					mv := h.mark * float64(h.trade.Quantity)
					floating := mv - totalCost
					day.Holdings = append(day.Holdings, ReplayHolding{
						PositionID:   h.positionID,
						Code:         h.buy.Code,
						BuyTime:      h.trade.BuyTime.Format(replayTimeLayout),
						Quantity:     h.trade.Quantity,
						BuyPrice:     h.trade.BuyPrice,
						BuyExecPrice: h.trade.BuyExecPrice,
						TotalCost:    totalCost,
						CostPrice:    totalCost / float64(h.trade.Quantity),
						MarkPrice:    h.mark,
						MarkDate:     h.markDate,
						MarketValue:  mv,
						FloatingPnL:  floating,
						FloatingRate: floating / totalCost * 100,
					})
				}
				day.Trades = s.replay.takeDayTrades()
				slices.SortStableFunc(day.Trades, func(a, b ReplayTrade) int {
					if a.Time < b.Time {
						return -1
					}
					if a.Time > b.Time {
						return 1
					}
					return 0
				})
				for i := range day.Trades {
					day.Trades[i].Seq = s.replay.nextSeq - len(day.Trades) + i + 1
				}
				s.replay.Dates = append(s.replay.Dates, date)
				s.replay.Days = append(s.replay.Days, day)
			}
		}
		// 仅保留仍有持仓的代码分钟缓存，避免全市场多年分钟数据常驻内存。
		for code := range minutes {
			held := false
			for _, s := range states {
				for _, h := range s.holdings {
					if h.buy.Code == code {
						held = true
					}
				}
			}
			if !held {
				delete(minutes, code)
			}
		}
		if cfg.OnCodeDone != nil {
			cfg.OnCodeDone(researchrun.Progress{Phase: "simulating", Done: day + 1, Total: len(index), Code: date})
		}
	}
	for i, s := range states {
		// 回测结束时冻结规则描述表；构建期已完整登记，此处只读快照。
		if s.replay != nil {
			s.replay.snapshotRules(contexts[i])
		}
		for _, h := range s.holdings {
			if h.exit != nil {
				s.report.PendingSells++
			}
			tr := h.trade
			tr.SellTime, tr.SellPrice, tr.SellExecPrice = accountCloseTime(index[len(index)-1].Time), h.mark, h.mark
			tr.SellIncome, tr.Virtual = h.mark*float64(tr.Quantity), true
			s.trades = append(s.trades, tr)
		}
		sort.SliceStable(s.trades, func(a, b int) bool { return s.trades[a].BuyTime.Before(s.trades[b].BuyTime) })
		result.Results[i].Trades = s.trades
	}
	return result, accounts, replays, benchmark, data, nil
}

func accountCloseTime(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 15, 0, 0, 0, t.Location())
}

func accountOpenTime(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 9, 30, 0, 0, t.Location())
}

func newAccountSignal(at time.Time, daily bool, replay *ReplayVariant, contexts []*builder.RecordContext, index int) *accountSignal {
	signal := &accountSignal{at: at, daily: daily}
	if replay != nil {
		signal.reason = contexts[index].ExtractSuccessPath()
	}
	return signal
}

func (s *accountState) canSignalEntry(code string) bool {
	for _, h := range s.holdings {
		if h.buy.Code == code {
			return h.exit != nil
		}
	}
	return true
}

func (s *accountState) buyAtOpen(signal accountBuySignal, ks extend.Klines, account AccountConfig, cost core.Cost) bool {
	if len(s.holdings) >= account.MaxPositions || len(ks) == 0 {
		return false
	}
	for _, h := range s.holdings {
		if h.buy.Code == signal.code {
			return false
		}
	}
	today := ks[len(ks)-1]
	if !canFillStockBar(signal.code, today.Kline, priorStockClose(ks), today.Open, true) {
		return false
	}
	budget := math.Min(account.OrderBudget, s.cash)
	price, _ := cost.BuyCost(today.Open, 100)
	if !validExecutionPrice(price) {
		return false
	}
	quantity := int(math.Floor(budget/price/100)) * 100
	for ; quantity > 0; quantity -= 100 {
		_, buyCost := cost.BuyCost(today.Open, quantity)
		if !validExecutionPrice(buyCost) || buyCost > budget {
			continue
		}
		s.cash -= buyCost
		s.nextPosID++
		at := accountOpenTime(today.Time)
		tr := core.Trade{Code: signal.code, BuyTime: at, BuyPrice: today.Open, BuyExecPrice: price, BuyCost: buyCost, Quantity: quantity}
		s.holdings = append(s.holdings, accountHolding{
			buy: core.Buy{Code: signal.code, Time: at, Price: today.Open}, trade: tr,
			mark: today.Open, positionID: s.nextPosID, markDate: at.Format(time.DateOnly),
		})
		if s.replay != nil {
			s.replay.recordTrade(ReplayTrade{
				PositionID: s.nextPosID, Code: tr.Code, Time: at.Format(replayTimeLayout),
				SignalTime: signal.at.Format(replayTimeLayout), Side: "buy", Price: tr.BuyPrice,
				ExecPrice: price, Quantity: quantity, Fee: buyCost - price*float64(quantity), Reason: signal.reason,
			})
		}
		return true
	}
	return false
}

func (s *accountState) sellAt(holding accountHolding, at time.Time, price float64, cost core.Cost) error {
	tr := holding.trade
	tr.SellTime, tr.SellPrice = at, price
	tr.SellExecPrice, tr.SellIncome = cost.SellIncome(price, tr.Quantity)
	if !validExecutionPrice(tr.SellExecPrice) || math.IsNaN(tr.SellIncome) || math.IsInf(tr.SellIncome, 0) || tr.SellIncome < 0 {
		return fmt.Errorf("%s 卖出价格或费用产生无效收入，请检查成本配置", holding.buy.Code)
	}
	s.cash += tr.SellIncome
	s.trades = append(s.trades, tr)
	if s.replay != nil {
		s.replay.recordTrade(ReplayTrade{
			PositionID: holding.positionID, Code: tr.Code, Time: at.Format(replayTimeLayout),
			SignalTime: holding.exit.at.Format(replayTimeLayout), Side: "sell", Price: price,
			ExecPrice: tr.SellExecPrice, Quantity: tr.Quantity, Fee: tr.SellExecPrice*float64(tr.Quantity) - tr.SellIncome,
			MinuteFallback: holding.exit.daily, Reason: holding.exit.reason,
		})
	}
	return nil
}
