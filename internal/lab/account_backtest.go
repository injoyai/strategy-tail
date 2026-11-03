package lab

import (
	"context"
	"fmt"
	"math"
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
}

type accountHolding struct {
	buy        core.Buy
	trade      core.Trade
	mark       float64
	positionID int
	markDate   string
}

type accountState struct {
	cash      float64
	holdings  []accountHolding
	trades    []core.Trade
	report    *AccountReport
	replay    *ReplayVariant
	nextPosID int
}

type accountMinutes struct {
	year int
	days map[string]protocol.Klines
}

// runAccountBacktest 逐日统一执行账户：盘中卖出、收盘买入、收盘估值。
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
		if k == nil || k.Kline == nil || k.Close <= 0.0 {
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
	// 代码排序只决定同一收盘时刻资金不足时的买入优先级，不使用未来收益排名。
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
			if k == nil || k.Kline == nil || k.Close <= 0.0 || (i > 0 && !k.Time.After(ks[i-1].Time)) {
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
		accounts[i] = &AccountReport{Points: make([]AccountPoint, 0, len(index))}
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
			remaining := make([]accountHolding, 0, len(s.holdings))
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
							if mk != nil && mk.Close > 0.0 {
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
				fallback := len(bars) == 0
				if fallback {
					s.report.MissingMinuteDays++
					bars = protocol.Klines{today.Kline}
				}
				// Seller 只能看到截至当前分钟的数据；不覆写真实日线收盘价。
				sellKs := append(extend.Klines(nil), ks...)
				current := *today
				sellKs[len(sellKs)-1] = &current
				sold := false
				for m, bar := range bars {
					if err := ctx.Err(); err != nil {
						return result, nil, nil, nil, nil, err
					}
					current.Kline = bars[:m+1].Kline(bar.Time, bar.Open)
					if fallback {
						current.Kline = today.Kline
					}
					if seller.Sell(holding.buy.Code, sellKs, holding.buy) {
						tr := holding.trade
						tr.SellTime = bar.Time
						if fallback {
							tr.SellTime = accountCloseTime(today.Time)
						}
						tr.SellPrice = current.Close
						tr.SellExecPrice, tr.SellIncome = cfg.Cost.SellIncome(current.Close, tr.Quantity)
						if tr.SellExecPrice <= 0.0 || tr.SellIncome < 0 {
							return result, nil, nil, nil, nil, fmt.Errorf("%s 卖出价格或费用产生负收入，请检查成本配置", holding.buy.Code)
						}
						s.cash += tr.SellIncome
						s.trades = append(s.trades, tr)
						// 旁路记录实际卖出事件；ExtractSuccessPath 返回新建切片，引用安全。
						if s.replay != nil {
							s.replay.recordTrade(ReplayTrade{
								PositionID:     holding.positionID,
								Code:           holding.buy.Code,
								Time:           tr.SellTime.Format(replayTimeLayout),
								Side:           "sell",
								Price:          tr.SellPrice,
								ExecPrice:      tr.SellExecPrice,
								Quantity:       tr.Quantity,
								Fee:            tr.SellExecPrice*float64(tr.Quantity) - tr.SellIncome,
								MinuteFallback: fallback,
								Reason:         contexts[i].ExtractSuccessPath(),
							})
						}
						sold = true
						break
					}
				}
				if !sold {
					remaining = append(remaining, holding)
				}
			}
			s.holdings = remaining
			for _, code := range codes {
				if len(s.holdings) >= account.MaxPositions {
					break
				}
				ks := prefixes[code]
				if len(ks) == 0 || ks[len(ks)-1].Volume <= 0 {
					continue
				}
				held := false
				for _, h := range s.holdings {
					if h.buy.Code == code {
						held = true
						break
					}
				}
				if held || !variant.Buyer.Buy(code, ks) {
					continue
				}
				today := ks[len(ks)-1]
				budget := math.Min(account.OrderBudget, s.cash)
				price, _ := cfg.Cost.BuyCost(today.Close, 100)
				if price <= 0.0 {
					return result, nil, nil, nil, nil, fmt.Errorf("%s 买入成交价无效", code)
				}
				qty := int(math.Floor(budget/price/100)) * 100
				var buyCost float64
				for qty > 0 {
					_, buyCost = cfg.Cost.BuyCost(today.Close, qty)
					if buyCost <= budget {
						break
					}
					qty -= 100
				}
				if qty <= 0 {
					continue
				}
				s.cash -= buyCost
				s.nextPosID++
				tr := core.Trade{Code: code, BuyTime: accountCloseTime(today.Time), BuyPrice: today.Close, BuyExecPrice: price, BuyCost: buyCost, Quantity: qty}
				s.holdings = append(s.holdings, accountHolding{buy: core.Buy{Code: code, Time: today.Time, Price: today.Close}, trade: tr, mark: today.Close, positionID: s.nextPosID, markDate: date})
				// 旁路记录实际买入事件；仓位身份此后固定，卖出沿用。
				if s.replay != nil {
					s.replay.recordTrade(ReplayTrade{
						PositionID: s.nextPosID,
						Code:       code,
						Time:       tr.BuyTime.Format(replayTimeLayout),
						Side:       "buy",
						Price:      today.Close,
						ExecPrice:  price,
						Quantity:   qty,
						Fee:        buyCost - price*float64(qty),
						Reason:     contexts[i].ExtractSuccessPath(),
					})
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
