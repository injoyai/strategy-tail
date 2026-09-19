// execution.go 组合执行状态机（v2 设计 §9.2/§9.3）。
//
// ExecuteDay 按固定顺序处理单日（对应设计 §9.2 的 1-6 步；第 7-8 步估值与对账见
// accounting.go）：
//
//  1. 期初快照并按前收盘统一口径估值（公司行为前的原始持仓/现金）；
//  2. 应用当日公司行为（分红/拆并股/退市），记录质量事件；
//  3. 依据停牌、涨跌停、上市状态与 T+1 规则确定可成交集合（数据缺失 fail closed，
//     并降低证据等级）；
//  4. 先执行可成交卖单（按目标缩减），扣除费用并释放现金；
//  5. 再按冻结优先规则（信号日分数降序，并列按代码升序）分配可用现金执行买单；
//  6. 整手向下取整，不允许负现金；
//  7. 每笔成交/拒绝/部分成交/未成交原因进入账本（未成交原因使用稳定枚举）。
//
// 确定性：不依赖 map 随机序、系统时间或并发；同一输入重复运行逐位一致。
// 同一日的信号（收盘后生成的目标）只影响次日开盘成交，不使用未来价格补成交。
// 未成交意图默认日终失效（CarryUnfilled=false）；为 true 时买单意图跨日保留
// （仅当新目标对该代码保持沉默时补买，避免与新研究意图冲突）。
package portfolioresearch

import (
	"fmt"
	"math"
	"sort"
)

// ---- 稳定枚举 ----

// 成交方向。
const (
	SideBuy  = "buy"
	SideSell = "sell"
)

// 公司行为类型（accounting.go 应用）。
const (
	CorporateActionDividend = "dividend" // 分红（现金流入，计入损益）
	CorporateActionSplit    = "split"    // 拆并股（股数×PerShare，成本基础不变）
	CorporateActionDelist   = "delist"   // 退市（当日不可交易，质量事件 + 降级）
)

// ---- 领域类型 ----

// FeeBreakdown 单笔成交费用明细（口径与 core/cost.go 一致）：
// 佣金双边、最低佣金；印花税仅卖出；过户费按配置双边。
type FeeBreakdown struct {
	Commission  float64 `json:"commission"`
	StampDuty   float64 `json:"stampDuty"`
	TransferFee float64 `json:"transferFee"`
}

// Total 费用合计。
func (f FeeBreakdown) Total() float64 { return f.Commission + f.StampDuty + f.TransferFee }

// HoldingLot 单笔持仓（同一代码可多笔，按买入日区分 T+1 可售）。
// CostBasis 为持仓成本基础 = 股数×买入成交价（不含费用）；费用在发生时单独
// 计入账本（对账恒等式把费用作为独立减项，见 accounting.go）。
type HoldingLot struct {
	Code      string  `json:"code"`
	Shares    int     `json:"shares"`
	BuyPrice  float64 `json:"buyPrice"`  // 买入成交价（含滑点，元/股）
	CostBasis float64 `json:"costBasis"` // 成本基础（元，不含费用）
	BuyDate   string  `json:"buyDate"`   // 买入日 YYYY-MM-DD（T+1 判定）
}

// PortfolioState 组合状态（现金 + 持仓 + 可选跨日保留的未成交买单意图）。
type PortfolioState struct {
	Date    string        `json:"date,omitempty"`
	Cash    float64       `json:"cash"`
	Lots    []HoldingLot  `json:"lots"`
	Pending []OrderIntent `json:"pending,omitempty"` // CarryUnfilled=true 时跨日保留的买单意图
}

// OrderIntent 单一目标意图（从目标组合与当前持仓推导，或跨日保留）。
type OrderIntent struct {
	Code     string  `json:"code"`
	Side     string  `json:"side"`
	Shares   int     `json:"shares"`
	Weight   float64 `json:"weight,omitempty"` // 目标权重（审计引用）
	Priority float64 `json:"priority"`         // 冻结优先级（信号日分数；现金分配用）
}

// Fill 成交记录（Sequence 为当日确定性事件序号）。
type Fill struct {
	Date        string       `json:"date"`
	Seq         int          `json:"seq"`
	Code        string       `json:"code"`
	Side        string       `json:"side"`
	Shares      int          `json:"shares"`
	Price       float64      `json:"price"`       // 成交价（含滑点，元/股）
	GrossAmount float64      `json:"grossAmount"` // 成交毛额（股数×价格）
	Fees        FeeBreakdown `json:"fees"`
	NetAmount   float64      `json:"netAmount"` // 净额：买入=总支出，卖出=净收入（元）
	Partial     bool         `json:"partial"`   // 是否部分成交
	Requested   int          `json:"requested"` // 请求股数（部分成交时 > Shares）
}

// Rejection 拒绝/未成交记录（Reason 使用 UnfilledReason 稳定枚举）。
type Rejection struct {
	Date   string `json:"date"`
	Seq    int    `json:"seq"`
	Code   string `json:"code"`
	Side   string `json:"side"`
	Reason string `json:"reason"`
	Shares int    `json:"shares"`
}

// DayMarket 执行日行情与可交易信息。
// fail closed：缺失即不可交易，并记录数据质量事件、降低证据等级。
type DayMarket struct {
	Date             string                       `json:"date"`
	OpenPrice        map[string]float64           `json:"openPrice"`        // 开盘价（元，成交基准价）
	PrevClose        map[string]float64           `json:"prevClose"`        // 前收盘价（元，期初估值参考）
	ClosePrice       map[string]float64           `json:"closePrice"`       // 收盘估值价（元；缺失退回 PrevClose）
	Tradable         map[string]bool              `json:"tradable"`         // 停牌标记（缺失=false，fail closed）
	LimitUp          map[string]bool              `json:"limitUp"`          // 涨停（买不到）
	LimitDown        map[string]bool              `json:"limitDown"`        // 跌停（卖不掉）
	Listed           map[string]bool              `json:"listed"`           // 上市状态（缺失=false → not_listed）
	Volume           map[string]float64           `json:"volume,omitempty"` // 可选成交额：仅容量告警，不参与定价（未建模）
	CorporateActions map[string][]CorporateAction `json:"corporateActions,omitempty"`
}

// DayInput 单日执行输入：前一收盘后目标组合 + 执行日行情/可交易信息 + 信号日分数。
// 对应任务输入分解：TargetPortfolio + 当日行情 + PortfolioState + ExecutionSpec
// （ExecutionSpec 内嵌 CostSpec；PortfolioState 作为 ExecuteDay 首个参数）。
type DayInput struct {
	Target TargetPortfolio    // 前一收盘后生成的目标组合（可执行目标 = Constrained.Shares）
	Market DayMarket          // 执行日行情/可交易信息
	Scores map[string]float64 // 信号日分数（冻结优先级，可选；缺失用目标有效权重，再缺用 0）
}

// ---- 执行引擎 ----

// ExecuteDay 执行单日（v2 设计 §9.2，先卖后买固定顺序）。返回当日账本与下一状态。
// state 作为只读输入，返回的 PortfolioState 为全新副本（含 Pending 跨日意图）。
func ExecuteDay(state PortfolioState, in DayInput, exec ExecutionSpec) (DailyLedger, PortfolioState, error) {
	if err := exec.Validate(); err != nil {
		return DailyLedger{}, PortfolioState{}, err
	}
	if in.Market.Date == "" {
		return DailyLedger{}, PortfolioState{}, fmt.Errorf("执行日行情缺少日期")
	}
	today := in.Market.Date

	// 期初快照（deep copy，避免污染入参；用于期初估值与对账）。
	beginCash := state.Cash
	beginLots := append([]HoldingLot(nil), state.Lots...)

	w := &execWork{
		exec:     exec,
		cost:     exec.Cost,
		market:   in.Market,
		today:    today,
		delisted: map[string]bool{},
	}

	// 1. 期初权益（公司行为前的原始持仓/现金，按前收盘统一口径估值）。
	ledger := DailyLedger{Date: today}
	beginEquity := w.beginEquity(beginCash, beginLots)

	// 2. 应用当日公司行为（分红/拆并股/退市）→ 工作副本；记录质量事件。
	//    applyCorporateActions 返回调整后的持仓与现金，由调用方赋值（无指针副作用）。
	workLots := append([]HoldingLot(nil), beginLots...)
	workCash := beginCash
	workLots, workCash = w.applyCorporateActions(workLots, workCash)

	// 3. 推导意图（目标缩减 + 跨日保留买单）。目标股数必须与公司行为调整后
	//    的持仓同口径（复权/公司行为已确认，设计 §9.5）。
	intents := buildIntents(workLots, in.Target.Constrained.Shares, in.Scores, in.Target.Constrained.Effective)
	carried := carriedBuys(state.Pending, intents)
	if exec.CarryUnfilled {
		intents = append(carried, intents...)
	} else {
		intents = append([]OrderIntent(nil), intents...)
	}
	sells, buys := splitIntents(intents)
	buys = orderBuys(buys) // 冻结优先级：分数降序，并列代码升序（确定性）。
	// 账本意图按实际执行顺序记录：卖出（代码升序）在前，买入（冻结优先级）在后。
	ledger.Intents = append(ledger.Intents, sells...)
	ledger.Intents = append(ledger.Intents, buys...)

	// 4. 先卖后买（固定顺序）。
	ws := PortfolioState{Cash: workCash, Lots: workLots}
	seq := 0
	nextSeq := func() int { seq++; return seq }
	var realized float64
	fees := FeeBreakdown{}

	for _, it := range sells { // 卖出按代码升序（确定性）。
		fill, rej, lotsAfter, net, rlz, fe, deg := w.executeSell(ws, it, nextSeq())
		ws.Lots = lotsAfter
		ws.Cash += net
		realized += rlz
		fees = addFee(fees, fe)
		if deg {
			ledger.Degraded = true
		}
		if fill != nil {
			ledger.Fills = append(ledger.Fills, *fill)
		}
		if rej != nil {
			ledger.Rejections = append(ledger.Rejections, *rej)
		}
	}

	buys = orderBuys(buys) // 冻结优先级：分数降序，并列代码升序（确定性）。
	var pending []OrderIntent
	for _, it := range buys {
		fill, rej, lotsAfter, cashAfter, fe, deg := w.executeBuy(ws.Cash, ws.Lots, it, nextSeq())
		ws.Lots = lotsAfter
		ws.Cash = cashAfter
		fees = addFee(fees, fe)
		if deg {
			ledger.Degraded = true
		}
		if fill != nil {
			ledger.Fills = append(ledger.Fills, *fill)
		}
		if rej != nil {
			ledger.Rejections = append(ledger.Rejections, *rej)
			// 跨日保留未成交（部分成交时只保留剩余股数），避免重复买入超目标。
			if exec.CarryUnfilled && rej.Shares > 0 {
				carried := it
				carried.Shares = rej.Shares
				pending = append(pending, carried)
			}
		}
	}
	if exec.CarryUnfilled {
		ws.Pending = pending
	} else {
		ws.Pending = nil
	}

	// 5. 期末估值与对账（accounting.go）。
	ledger.BeginCash = beginCash
	ledger.EndCash = ws.Cash
	ledger.BeginHoldings = beginLots
	ledger.EndHoldings = append([]HoldingLot(nil), ws.Lots...)
	ledger.BeginEquity = beginEquity
	ledger.Fees = fees
	ledger.RealizedPnL = realized
	ledger.CorporateIncome = w.dividendIncome
	ledger.EndEquity = w.endEquity(ws.Cash, ws.Lots)
	ledger.UnrealizedPnL = w.unrealizedChange(beginLots, ws.Lots)
	ledger.Pnl = ledger.RealizedPnL + ledger.CorporateIncome + ledger.UnrealizedPnL
	ledger.QualityEvents = w.qualityEvents
	for _, e := range w.qualityEvents {
		if e.Degraded {
			ledger.Degraded = true
		}
	}
	ledger.Reconciled, ledger.ReconResidual = Reconcile(ledger.BeginEquity, ledger.Pnl, ledger.Fees.Total(), ledger.EndEquity)

	ws.Date = today
	return ledger, ws, nil
}

// execWork 单日执行上下文（ExecuteDay 局部状态，无包级可变状态）。
type execWork struct {
	exec           ExecutionSpec
	cost           CostSpec
	market         DayMarket
	today          string
	dividendIncome float64
	qualityEvents  []ExecutionQualityEvent
	delisted       map[string]bool // 当日退市代码（不可交易 + 降级）
}

// markQuality 记录数据质量事件。
func (w *execWork) markQuality(typ, code, detail string, degraded bool) {
	w.qualityEvents = append(w.qualityEvents, ExecutionQualityEvent{
		Date: w.today, Code: code, Type: typ, Detail: detail, Degraded: degraded,
	})
}

// ---- 意图推导 ----

// buildIntents 从持仓与目标股数推导买卖意图：
// 目标股数 > 当前 → 买入；目标股数 < 当前 → 卖出；目标无该代码 → 清仓卖出。
// 确定性：卖出按代码升序，买入按冻结优先级（见 orderBuys）。
func buildIntents(lots []HoldingLot, targetShares map[string]int, scores, effective map[string]float64) []OrderIntent {
	cur := map[string]int{}
	for _, l := range lots {
		cur[l.Code] += l.Shares
	}
	codes := map[string]bool{}
	for c := range cur {
		codes[c] = true
	}
	for c := range targetShares {
		codes[c] = true
	}
	var sells, buys []OrderIntent
	for _, c := range sortedCodes(codes) {
		curShares := cur[c]
		tgtShares := targetShares[c]
		priority := intentPriority(c, scores, effective)
		if tgtShares > curShares {
			buys = append(buys, OrderIntent{Code: c, Side: SideBuy, Shares: tgtShares - curShares, Priority: priority})
		} else if tgtShares < curShares {
			sells = append(sells, OrderIntent{Code: c, Side: SideSell, Shares: curShares - tgtShares, Priority: priority})
		}
	}
	return append(sells, buys...)
}

// intentPriority 冻结优先级 = 信号日分数（缺失用目标有效权重，再缺用 0），并列按代码升序。
func intentPriority(code string, scores, effective map[string]float64) float64 {
	if scores != nil {
		if v, ok := scores[code]; ok && !math.IsNaN(v) && !math.IsInf(v, 0) {
			return v
		}
	}
	if effective != nil {
		if v, ok := effective[code]; ok && !math.IsNaN(v) && !math.IsInf(v, 0) {
			return v
		}
	}
	return 0
}

// carriedBuys 跨日保留的买单意图：仅当新目标对该代码保持沉默（不产生买入/卖出
// 意图）时补入，避免与新的研究意图冲突；同代码以新目标为准。
func carriedBuys(pending []OrderIntent, fresh []OrderIntent) []OrderIntent {
	freshCodes := map[string]bool{}
	for _, it := range fresh {
		freshCodes[it.Code] = true
	}
	var out []OrderIntent
	for _, it := range pending {
		if it.Side == SideBuy && !freshCodes[it.Code] {
			out = append(out, it)
		}
	}
	return out
}

// splitIntents 把意图按方向拆分（卖出在前，买入在后）。
func splitIntents(intents []OrderIntent) ([]OrderIntent, []OrderIntent) {
	var sells, buys []OrderIntent
	for _, it := range intents {
		if it.Side == SideSell {
			sells = append(sells, it)
		} else {
			buys = append(buys, it)
		}
	}
	return sells, buys
}

// orderBuys 按冻结优先级排序买单：分数降序，并列按代码升序（确定性）。
func orderBuys(buys []OrderIntent) []OrderIntent {
	sort.SliceStable(buys, func(i, j int) bool {
		if buys[i].Priority != buys[j].Priority {
			return buys[i].Priority > buys[j].Priority
		}
		return buys[i].Code < buys[j].Code
	})
	return buys
}

// ---- 卖出 ----

// executeSell 执行单个卖出意图。返回：成交（可空）、拒绝（可空）、消耗后的持仓、
// 净现金流入、已实现损益、费用与是否降级。
func (w *execWork) executeSell(state PortfolioState, it OrderIntent, seq int) (*Fill, *Rejection, []HoldingLot, float64, float64, FeeBreakdown, bool) {
	lots := state.Lots
	deg := false

	if reason, blocked, degraded := w.sellBlocked(it.Code); blocked {
		if degraded {
			deg = true
		}
		return nil, &Rejection{Date: w.today, Seq: seq, Code: it.Code, Side: SideSell, Reason: reason, Shares: it.Shares},
			lots, 0, 0, FeeBreakdown{}, deg
	}
	// 缺价：fail closed（按停牌处理）+ 质量事件 + 降级（不补造价格）。
	open, ok := w.market.OpenPrice[it.Code]
	if !ok || math.IsNaN(open) || math.IsInf(open, 0) || open <= 0 {
		deg = true
		w.markQuality("missing_market_data", it.Code, "缺开盘价（fail closed，不补造价格）", true)
		return nil, &Rejection{Date: w.today, Seq: seq, Code: it.Code, Side: SideSell, Reason: UnfilledSuspended, Shares: it.Shares},
			lots, 0, 0, FeeBreakdown{}, deg
	}

	sellable := sellableShares(state, it.Code, w.today, w.exec.T1Restriction)
	desired := it.Shares
	if desired > sellable {
		desired = sellable
	}
	if desired <= 0 {
		return nil, &Rejection{Date: w.today, Seq: seq, Code: it.Code, Side: SideSell, Reason: UnfilledT1Restricted, Shares: it.Shares},
			lots, 0, 0, FeeBreakdown{}, false
	}
	// 整手向下取整（卖出防御：目标股数理论已整手）。
	qty := (desired / w.exec.LotSize) * w.exec.LotSize
	if qty <= 0 {
		return nil, &Rejection{Date: w.today, Seq: seq, Code: it.Code, Side: SideSell, Reason: UnfilledLotRounding, Shares: it.Shares},
			lots, 0, 0, FeeBreakdown{}, false
	}
	execPrice := open - w.cost.Slippage
	if execPrice < 0 {
		execPrice = 0
	}
	gross := float64(qty) * execPrice
	fe := sellFees(gross, w.cost)
	net := gross - fe.Total()
	lots, costSold := consumeShares(lots, it.Code, qty)

	fill := &Fill{
		Date: w.today, Seq: seq, Code: it.Code, Side: SideSell,
		Shares: qty, Price: execPrice, GrossAmount: gross, Fees: fe, NetAmount: net,
	}
	realized := gross - costSold
	// 剩余（可售不足 / 整手取整）记录拒绝。
	var rej *Rejection
	remainder := it.Shares - qty
	if remainder > 0 {
		reason := UnfilledT1Restricted
		if it.Shares%w.exec.LotSize != 0 && sellable >= it.Shares {
			reason = UnfilledLotRounding
		}
		rej = &Rejection{Date: w.today, Seq: seq, Code: it.Code, Side: SideSell, Reason: reason, Shares: remainder}
	}
	return fill, rej, lots, net, realized, fe, deg
}

// sellBlocked 返回卖出被阻断的原因（blocked=true 表示阻断）。
// fail closed：上市状态/停牌标记缺失均视为阻断并降级。
func (w *execWork) sellBlocked(code string) (string, bool, bool) {
	if w.delisted[code] {
		return UnfilledNotListed, true, true
	}
	listed, hasListed := w.market.Listed[code]
	if !hasListed {
		w.markQuality("missing_market_data", code, "上市状态缺失（fail closed）", true)
		return UnfilledSuspended, true, true
	}
	if !listed {
		w.markQuality("delisted", code, "退市/未上市（当日不可交易，不补造价格）", true)
		return UnfilledNotListed, true, true
	}
	tradable, hasTradable := w.market.Tradable[code]
	if !hasTradable {
		w.markQuality("missing_market_data", code, "停牌状态缺失（fail closed）", true)
		return UnfilledSuspended, true, true
	}
	if !tradable {
		return UnfilledSuspended, true, false
	}
	if w.market.LimitDown[code] {
		return UnfilledLimitDown, true, false
	}
	return "", false, false
}

// ---- 买入 ----

// executeBuy 执行单个买入意图。返回：成交（可空）、拒绝（可空）、买入后的持仓
// （新增 lot 追加在返回切片尾部）、买入后的现金、费用与是否降级。
// 无指针副作用（PROJECT_RULES §7）：入参 cash/lots 按值传入，结果经返回值显式
// 回传，由调用方赋值回状态。
func (w *execWork) executeBuy(cash float64, lots []HoldingLot, it OrderIntent, seq int) (*Fill, *Rejection, []HoldingLot, float64, FeeBreakdown, bool) {
	deg := false

	if reason, blocked, degraded := w.buyBlocked(it.Code); blocked {
		if degraded {
			deg = true
		}
		return nil, &Rejection{Date: w.today, Seq: seq, Code: it.Code, Side: SideBuy, Reason: reason, Shares: it.Shares},
			lots, cash, FeeBreakdown{}, deg
	}
	open, ok := w.market.OpenPrice[it.Code]
	if !ok || math.IsNaN(open) || math.IsInf(open, 0) || open <= 0 {
		deg = true
		w.markQuality("missing_market_data", it.Code, "缺开盘价（fail closed，不补造价格）", true)
		return nil, &Rejection{Date: w.today, Seq: seq, Code: it.Code, Side: SideBuy, Reason: UnfilledSuspended, Shares: it.Shares},
			lots, cash, FeeBreakdown{}, deg
	}
	buyPrice := open + w.cost.Slippage

	// 可负担整手数（精确费用校验，不允许负现金）。
	maxLots := maxAffordableLots(cash, buyPrice, w.cost, w.exec.LotSize)
	maxQty := maxLots * w.exec.LotSize
	qty := it.Shares
	if qty > maxQty {
		qty = maxQty
	}
	qty = (qty / w.exec.LotSize) * w.exec.LotSize // 整手向下取整
	if qty <= 0 {
		return nil, &Rejection{Date: w.today, Seq: seq, Code: it.Code, Side: SideBuy, Reason: UnfilledInsufficientCash, Shares: it.Shares},
			lots, cash, FeeBreakdown{}, false
	}
	gross := float64(qty) * buyPrice
	fe := buyFees(gross, w.cost)
	total := gross + fe.Total()
	if total > cash+1e-9 {
		// 防御：maxAffordableLots 已保证不超支；此处兜底防浮点边界。
		return nil, &Rejection{Date: w.today, Seq: seq, Code: it.Code, Side: SideBuy, Reason: UnfilledInsufficientCash, Shares: it.Shares},
			lots, cash, FeeBreakdown{}, false
	}
	cash -= total

	fill := &Fill{
		Date: w.today, Seq: seq, Code: it.Code, Side: SideBuy,
		Shares: qty, Price: buyPrice, GrossAmount: gross, Fees: fe, NetAmount: total,
	}
	var rej *Rejection
	remainder := it.Shares - qty
	if remainder > 0 {
		reason := UnfilledInsufficientCash
		if maxQty >= it.Shares && it.Shares%w.exec.LotSize != 0 {
			reason = UnfilledLotRounding
		}
		rej = &Rejection{Date: w.today, Seq: seq, Code: it.Code, Side: SideBuy, Reason: reason, Shares: remainder}
		fill.Partial = true
		fill.Requested = it.Shares
	}
	// 容量告警：成交毛额超过当日成交额阈值时不调整价格，仅记录质量事件（明确未建模）。
	if vol, ok := w.market.Volume[it.Code]; ok && vol > 0 && gross > capacityParticipation*vol {
		w.markQuality("capacity", it.Code, fmt.Sprintf("成交毛额 %.2f 超过当日成交额 %.0f 的 %.1f%%（容量未建模，不调整价格）",
			gross, vol, capacityParticipation*100), false)
	}
	lots = append(lots, HoldingLot{
		Code: it.Code, Shares: qty, BuyPrice: buyPrice, CostBasis: gross, BuyDate: w.today,
	})
	return fill, rej, lots, cash, fe, deg
}

// buyBlocked 返回买入被阻断的原因（blocked=true 表示阻断）。
func (w *execWork) buyBlocked(code string) (string, bool, bool) {
	if w.delisted[code] {
		return UnfilledNotListed, true, true
	}
	listed, hasListed := w.market.Listed[code]
	if !hasListed {
		w.markQuality("missing_market_data", code, "上市状态缺失（fail closed）", true)
		return UnfilledSuspended, true, true
	}
	if !listed {
		w.markQuality("delisted", code, "退市/未上市（当日不可交易，不补造价格）", true)
		return UnfilledNotListed, true, true
	}
	tradable, hasTradable := w.market.Tradable[code]
	if !hasTradable {
		w.markQuality("missing_market_data", code, "停牌状态缺失（fail closed）", true)
		return UnfilledSuspended, true, true
	}
	if !tradable {
		return UnfilledSuspended, true, false
	}
	if w.market.LimitUp[code] {
		return UnfilledLimitUp, true, false
	}
	return "", false, false
}

// capacityParticipation 容量告警阈值：成交毛额超过当日成交额该比例时告警。
const capacityParticipation = 0.01 // 1%（默认；仅告警，不参与定价）

// ---- 费用 ----

// commissionFee 佣金 = max(毛额×费率, 最低佣金)。
func commissionFee(gross float64, c CostSpec) float64 {
	f := gross * c.CommissionRate
	if c.MinCommission > 0 && f < c.MinCommission {
		return c.MinCommission
	}
	return f
}

// buyFees 买入费用 = 佣金 + 过户费（双边，按配置）。
func buyFees(gross float64, c CostSpec) FeeBreakdown {
	return FeeBreakdown{Commission: commissionFee(gross, c), TransferFee: gross * c.TransferFeeRate}
}

// sellFees 卖出费用 = 佣金 + 印花税 + 过户费（印花税仅卖出）。
func sellFees(gross float64, c CostSpec) FeeBreakdown {
	return FeeBreakdown{
		Commission:  commissionFee(gross, c),
		StampDuty:   gross * c.StampDutyRate,
		TransferFee: gross * c.TransferFeeRate,
	}
}

// addFee 累加两笔费用明细。
func addFee(a, b FeeBreakdown) FeeBreakdown {
	a.Commission += b.Commission
	a.StampDuty += b.StampDuty
	a.TransferFee += b.TransferFee
	return a
}

// ---- 可售数量 / 整手 ----

// sellableShares 某代码当日可售数量。
// T+1 开启：可售 = 当日之前买入的持仓（排除当日买入）；关闭：全部持仓。
func sellableShares(state PortfolioState, code string, today string, t1 bool) int {
	n := 0
	for _, l := range state.Lots {
		if l.Code != code {
			continue
		}
		if t1 && l.BuyDate == today {
			continue
		}
		n += l.Shares
	}
	return n
}

// consumeShares 按买入日升序（FIFO）从持仓中消耗 shares 股（同一代码），
// 返回消耗后的持仓（剔除零股）与被消耗的成本基础。确定性：同日按原顺序。
func consumeShares(lots []HoldingLot, code string, qty int) ([]HoldingLot, float64) {
	idx := make([]int, 0)
	for i, l := range lots {
		if l.Code == code {
			idx = append(idx, i)
		}
	}
	sort.SliceStable(idx, func(a, b int) bool { return lots[idx[a]].BuyDate < lots[idx[b]].BuyDate })
	remaining := qty
	cost := 0.0
	for _, i := range idx {
		if remaining <= 0 {
			break
		}
		l := &lots[i]
		take := l.Shares
		if take > remaining {
			take = remaining
		}
		perShare := 0.0
		if l.Shares > 0 {
			perShare = l.CostBasis / float64(l.Shares)
		}
		cost += perShare * float64(take)
		l.Shares -= take
		l.CostBasis -= perShare * float64(take)
		remaining -= take
	}
	out := lots[:0]
	for _, l := range lots {
		if l.Shares > 0 {
			out = append(out, l)
		}
	}
	return out, cost
}

// maxAffordableLots 给定可用现金与买入价（含滑点）下可负担的最大整手数。
// 用线性估计上界后按精确费用函数向下校准（最低佣金使精确成本 ≥ 线性估计），
// 保证不超支且确定性。
func maxAffordableLots(cash, buyPrice float64, c CostSpec, lot int) int {
	if buyPrice <= 0 || lot <= 0 || cash <= 0 {
		return 0
	}
	perShare := buyPrice * (1 + c.CommissionRate + c.TransferFeeRate)
	if perShare <= 0 {
		return 0
	}
	lots := int(math.Floor(cash / perShare / float64(lot)))
	if lots < 0 {
		lots = 0
	}
	for lots > 0 {
		q := lots * lot
		gross := float64(q) * buyPrice
		fe := buyFees(gross, c)
		if gross+fe.Total() <= cash+1e-9 {
			break
		}
		lots--
	}
	return lots
}

// ---- 多日驱动 ----

// Simulate 逐日执行（v2 设计 §9.2 第 1-8 步的多日循环）。
// days[i] 的 Target 为 days[i-1] 收盘后生成的目标（首个为期初前一日目标）。
// 返回逐日账本（长度 = len(days)）与最终状态。每日期初权益 = 上一日期末权益。
func Simulate(start PortfolioState, days []DayInput, exec ExecutionSpec) ([]DailyLedger, PortfolioState, error) {
	state := start
	ledgers := make([]DailyLedger, 0, len(days))
	for _, in := range days {
		l, next, err := ExecuteDay(state, in, exec)
		if err != nil {
			return ledgers, state, err
		}
		ledgers = append(ledgers, l)
		state = next
	}
	return ledgers, state, nil
}

// sortedCodes map 键升序（确定性迭代）。
func sortedCodes(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
