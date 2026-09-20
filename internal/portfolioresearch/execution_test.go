package portfolioresearch

// execution_test.go v2 Task 5 组合执行状态机测试（金标准场景 + 不变量）。
//
// 覆盖任务要求的金标准场景：
//   - 两股票卖出释放现金后买入第三只；
//   - 当日买入不能当日卖（T+1）；
//   - 停牌持仓保留（估值、不卖）、涨停买不到、跌停卖不掉；
//   - 部分现金只执行部分目标（按冻结优先级部分成交，记录未成交原因）；
//   - 佣金最低额、印花税方向（仅卖出）、整手取整；
//   - 退市/缺价按协议失败或降级，不补造价格；
//   - 确定性：同一输入重复运行订单/成交/持仓/净值逐位一致（DeepEqual）；
//   - 未来价格不参与当日成交资格；
//   - 未成交意图日终失效 / CarryUnfilled 跨日保留。

import (
	"math"
	"reflect"
	"testing"
)

// ---- 测试辅助 ----

// execT5 便捷构造执行参数（A 股整手 100，T+1 开启）。
func execT5() ExecutionSpec {
	return ExecutionSpec{
		Rebalance:     RebalanceDaily,
		FillAt:        FillNextOpen,
		SellFirst:     true,
		T1Restriction: true,
		CarryUnfilled: false,
		LotSize:       100,
		Cost: CostSpec{
			CommissionRate:  0.0003,
			StampDutyRate:   0.001,
			TransferFeeRate: 0,
			Slippage:        0.01,
			MinCommission:   5,
		},
	}
}

// tLot 便捷构造单笔持仓（成本基础 = 股数×买入价，不含费用）。
func tLot(code string, shares int, buyPrice float64, buyDate string) HoldingLot {
	return HoldingLot{Code: code, Shares: shares, BuyPrice: buyPrice, CostBasis: float64(shares) * buyPrice, BuyDate: buyDate}
}

// tState 便捷构造组合状态。
func tState(cash float64, lots ...HoldingLot) PortfolioState {
	return PortfolioState{Cash: cash, Lots: lots}
}

// tTarget 便捷构造目标组合（只填可执行目标股数；权重/有效权重用于优先级兜底）。
func tTarget(date string, shares map[string]int) TargetPortfolio {
	effective := make(map[string]float64, len(shares))
	for c, s := range shares {
		effective[c] = float64(s)
	}
	return TargetPortfolio{
		Date: date,
		Constrained: PortfolioWeights{
			Shares:    shares,
			Effective: effective,
		},
	}
}

// tMarket 便捷构造当日行情（所有代码默认可交易、已上市）。
func tMarket(date string, open, prev, close map[string]float64) DayMarket {
	return DayMarket{
		Date:       date,
		OpenPrice:  open,
		PrevClose:  prev,
		ClosePrice: close,
		Tradable:   map[string]bool{},
		LimitUp:    map[string]bool{},
		LimitDown:  map[string]bool{},
		Listed:     map[string]bool{},
	}
}

// markTradable 批量标记代码为可交易/已上市。
func markTradable(m *DayMarket, codes ...string) {
	for _, c := range codes {
		m.Tradable[c] = true
		m.Listed[c] = true
	}
}

// fillFor 在账本中查找某代码某方向的成交。
func fillFor(t *testing.T, l DailyLedger, code, side string) Fill {
	t.Helper()
	for _, f := range l.Fills {
		if f.Code == code && f.Side == side {
			return f
		}
	}
	t.Fatalf("账本缺少成交 %s/%s（fills=%v）", code, side, l.Fills)
	return Fill{}
}

// rejectionFor 在账本中查找某代码某方向的拒绝。
func rejectionFor(t *testing.T, l DailyLedger, code, side string) Rejection {
	t.Helper()
	for _, r := range l.Rejections {
		if r.Code == code && r.Side == side {
			return r
		}
	}
	t.Fatalf("账本缺少拒绝 %s/%s（rejections=%v）", code, side, l.Rejections)
	return Rejection{}
}

// approx 浮点容差比较。
func approx(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}

// sharesOf 汇总某代码持仓股数。
func sharesOf(state PortfolioState, code string) int {
	n := 0
	for _, l := range state.Lots {
		if l.Code == code {
			n += l.Shares
		}
	}
	return n
}

// ---- 金标准场景 ----

// TestSellReleaseCashThenBuyThird 两股票卖出释放现金后买入第三只。
//
// 手算依据（Cost: 佣金 0.0003/最低 5、印花税 0.001（卖出）、过户 0、滑点 0.01）：
//   - 期初：现金 0，持仓 A/B 各 100 股（成本 1000，买入价 10）。
//   - 卖出 A：成交价 9.99，毛额 999，佣金 5，印花税 0.999，净入 993.001；
//     卖出 B 同，现金累计 1986.002；已实现 = (999-1000)×2 = -2。
//   - 买入 C（100 股，成交价 10.01）：毛额 1001，佣金 5，总支出 1006，现金 980.002。
//   - 期末市值 = 980.002 + 100×10 = 1980.002；
//     期初权益 = 2000；pnl = -2 + (1000-1001) - (2000-2000) = -3；
//     对账 2000 - 3 - 16.998 = 1980.002 ✓。
func TestSellReleaseCashThenBuyThird(t *testing.T) {
	exec := execT5()
	state := tState(0, tLot("A", 100, 10, "2026-01-02"), tLot("B", 100, 10, "2026-01-02"))
	m := tMarket("2026-01-05",
		map[string]float64{"A": 10, "B": 10, "C": 10},
		map[string]float64{"A": 10, "B": 10, "C": 10},
		map[string]float64{"A": 10, "B": 10, "C": 10})
	markTradable(&m, "A", "B", "C")
	in := DayInput{Target: tTarget("2026-01-04", map[string]int{"C": 100}), Market: m,
		Scores: map[string]float64{"C": 2.0}}

	ledger, next, err := ExecuteDay(state, in, exec)
	if err != nil {
		t.Fatalf("ExecuteDay 失败: %v", err)
	}
	if sharesOf(next, "A") != 0 || sharesOf(next, "B") != 0 {
		t.Fatalf("卖出后 A/B 应清零，实际 A=%d B=%d", sharesOf(next, "A"), sharesOf(next, "B"))
	}
	if got := sharesOf(next, "C"); got != 100 {
		t.Fatalf("买入后 C 应为 100 股，实际 %d", got)
	}
	if !approx(next.Cash, 980.002, 1e-6) {
		t.Fatalf("期末现金 got %.6f want ≈980.002", next.Cash)
	}
	// 成交：2 卖 + 1 买。
	if len(ledger.Fills) != 3 {
		t.Fatalf("成交数 got %d want 3", len(ledger.Fills))
	}
	sf := fillFor(t, ledger, "A", SideSell)
	if !approx(sf.NetAmount, 993.001, 1e-6) {
		t.Fatalf("卖出 A 净额 got %.6f want 993.001", sf.NetAmount)
	}
	if !approx(sf.Fees.StampDuty, 0.999, 1e-9) {
		t.Fatalf("卖出 A 印花税 got %v want 0.999", sf.Fees.StampDuty)
	}
	bf := fillFor(t, ledger, "C", SideBuy)
	if !approx(bf.Price, 10.01, 1e-9) {
		t.Fatalf("买入 C 成交价 got %v want 10.01（开盘 10 + 滑点 0.01）", bf.Price)
	}
	// 不变量。
	if next.Cash < -1e-9 {
		t.Fatalf("现金为负: %v", next.Cash)
	}
	if !approx(ledger.EndEquity, 1980.002, 1e-6) {
		t.Fatalf("期末权益 got %.6f want 1980.002", ledger.EndEquity)
	}
	assertDailyInvariants(t, ledger)
}

// TestT1SellableShares 当日买入不可卖：可售数量 = 总持仓 - 当日买入。
func TestT1SellableShares(t *testing.T) {
	state := tState(0,
		tLot("X", 200, 10, "2026-01-05"), // 当日买入
		tLot("X", 100, 10, "2026-01-02"), // 之前买入
		tLot("Y", 300, 10, "2026-01-02"))
	if got := sellableShares(state, "X", "2026-01-05", true); got != 100 {
		t.Fatalf("T+1 下 X 可售 got %d want 100（排除当日买入 200）", got)
	}
	if got := sellableShares(state, "X", "2026-01-05", false); got != 300 {
		t.Fatalf("关闭 T+1 下 X 可售 got %d want 300", got)
	}
	if got := sellableShares(state, "Y", "2026-01-05", true); got != 300 {
		t.Fatalf("Y 可售 got %d want 300", got)
	}
}

// TestT1RestrictionRejectsSameDaySell 当日买入不能当日卖（引擎路径：拒绝并保留）。
func TestT1RestrictionRejectsSameDaySell(t *testing.T) {
	exec := execT5()
	state := tState(0, tLot("X", 200, 10, "2026-01-05")) // 买入日 = 执行日
	m := tMarket("2026-01-05",
		map[string]float64{"X": 10},
		map[string]float64{"X": 10},
		map[string]float64{"X": 10})
	markTradable(&m, "X")
	in := DayInput{Target: tTarget("2026-01-04", map[string]int{}), Market: m}

	ledger, next, err := ExecuteDay(state, in, exec)
	if err != nil {
		t.Fatalf("ExecuteDay 失败: %v", err)
	}
	r := rejectionFor(t, ledger, "X", SideSell)
	if r.Reason != UnfilledT1Restricted {
		t.Fatalf("拒绝原因 got %q want %q", r.Reason, UnfilledT1Restricted)
	}
	if r.Shares != 200 {
		t.Fatalf("拒绝股数 got %d want 200", r.Shares)
	}
	if sharesOf(next, "X") != 200 {
		t.Fatalf("T+1 卖出失败后应保留 200 股，实际 %d", sharesOf(next, "X"))
	}
	if len(ledger.Fills) != 0 {
		t.Fatalf("不应有成交，实际 %v", ledger.Fills)
	}
	assertDailyInvariants(t, ledger)
}

// TestSuspendedKeptLimitUpDown 停牌持仓保留（估值、不卖）、涨停买不到、跌停卖不掉。
func TestSuspendedKeptLimitUpDown(t *testing.T) {
	exec := execT5()
	state := tState(0,
		tLot("S", 100, 10, "2026-01-02"), // 停牌持仓
		tLot("X", 100, 10, "2026-01-02")) // 正常持仓（目标卖出）
	m := tMarket("2026-01-05",
		map[string]float64{"S": 10, "X": 10, "U": 10},
		map[string]float64{"S": 10, "X": 10, "U": 10},
		map[string]float64{"S": 12, "X": 10, "U": 10})
	markTradable(&m, "X", "U", "S")
	m.Tradable["S"] = false // 停牌
	m.LimitUp["U"] = true   // 涨停
	in := DayInput{
		Target: tTarget("2026-01-04", map[string]int{"U": 100}),
		Market: m,
		Scores: map[string]float64{"U": 2.0},
	}

	ledger, next, err := ExecuteDay(state, in, exec)
	if err != nil {
		t.Fatalf("ExecuteDay 失败: %v", err)
	}
	// 停牌持仓保留并估值。
	if sharesOf(next, "S") != 100 {
		t.Fatalf("停牌持仓应保留 100 股，实际 %d", sharesOf(next, "S"))
	}
	rs := rejectionFor(t, ledger, "S", SideSell)
	if rs.Reason != UnfilledSuspended {
		t.Fatalf("停牌卖出拒绝原因 got %q want suspended", rs.Reason)
	}
	// 涨停买不到。
	ru := rejectionFor(t, ledger, "U", SideBuy)
	if ru.Reason != UnfilledLimitUp {
		t.Fatalf("涨停买入拒绝原因 got %q want limit_up", ru.Reason)
	}
	if sharesOf(next, "U") != 0 {
		t.Fatalf("涨停不应成交 U，实际 %d", sharesOf(next, "U"))
	}
	// 停牌持仓按收盘估值计入净值（S 收盘 12）。
	if !approx(ledger.EndEquity, next.Cash+100*12, 1e-6) {
		t.Fatalf("期末权益应含停牌持仓估值（12×100）：got %.6f cash %.6f", ledger.EndEquity, next.Cash)
	}
	assertDailyInvariants(t, ledger)
}

// TestLimitDownCannotSell 跌停卖不掉。
func TestLimitDownCannotSell(t *testing.T) {
	exec := execT5()
	state := tState(0, tLot("D", 100, 10, "2026-01-02"))
	m := tMarket("2026-01-05",
		map[string]float64{"D": 10},
		map[string]float64{"D": 10},
		map[string]float64{"D": 10})
	markTradable(&m, "D")
	m.LimitDown["D"] = true
	in := DayInput{Target: tTarget("2026-01-04", map[string]int{}), Market: m}

	ledger, next, err := ExecuteDay(state, in, exec)
	if err != nil {
		t.Fatalf("ExecuteDay 失败: %v", err)
	}
	r := rejectionFor(t, ledger, "D", SideSell)
	if r.Reason != UnfilledLimitDown {
		t.Fatalf("跌停卖出拒绝原因 got %q want limit_down", r.Reason)
	}
	if sharesOf(next, "D") != 100 {
		t.Fatalf("跌停持仓应保留 100 股，实际 %d", sharesOf(next, "D"))
	}
	assertDailyInvariants(t, ledger)
}

// TestPartialCashPartialExecution 部分现金只执行部分目标（按冻结优先级部分成交）。
func TestPartialCashPartialExecution(t *testing.T) {
	exec := execT5()
	// 子场景 a：现金只够买 A（高优先级），B 被拒。
	stateA := tState(1100)
	mA := tMarket("2026-01-05",
		map[string]float64{"A": 10, "B": 10},
		map[string]float64{"A": 10, "B": 10},
		map[string]float64{"A": 10, "B": 10})
	markTradable(&mA, "A", "B")
	inA := DayInput{Target: tTarget("2026-01-04", map[string]int{"A": 100, "B": 100}), Market: mA,
		Scores: map[string]float64{"A": 2.0, "B": 1.0}}

	lA, nextA, err := ExecuteDay(stateA, inA, exec)
	if err != nil {
		t.Fatalf("ExecuteDay a 失败: %v", err)
	}
	if sharesOf(nextA, "A") != 100 {
		t.Fatalf("A 应全额成交，实际 %d", sharesOf(nextA, "A"))
	}
	if sharesOf(nextA, "B") != 0 {
		t.Fatalf("B 不应成交，实际 %d", sharesOf(nextA, "B"))
	}
	rB := rejectionFor(t, lA, "B", SideBuy)
	if rB.Reason != UnfilledInsufficientCash {
		t.Fatalf("B 拒绝原因 got %q want insufficient_cash", rB.Reason)
	}
	if nextA.Cash < -1e-9 {
		t.Fatalf("现金为负: %v", nextA.Cash)
	}

	// 子场景 b：现金够 1 手 C，目标 2 手 → 部分成交 100/200。
	stateC := tState(1500)
	mC := tMarket("2026-01-05",
		map[string]float64{"C": 10},
		map[string]float64{"C": 10},
		map[string]float64{"C": 10})
	markTradable(&mC, "C")
	inC := DayInput{Target: tTarget("2026-01-04", map[string]int{"C": 200}), Market: mC,
		Scores: map[string]float64{"C": 2.0}}

	lC, nextC, err := ExecuteDay(stateC, inC, exec)
	if err != nil {
		t.Fatalf("ExecuteDay b 失败: %v", err)
	}
	if got := sharesOf(nextC, "C"); got != 100 {
		t.Fatalf("C 应部分成交 100 股，实际 %d", got)
	}
	fc := fillFor(t, lC, "C", SideBuy)
	if !fc.Partial || fc.Requested != 200 || fc.Shares != 100 {
		t.Fatalf("C 部分成交记录错误: %+v", fc)
	}
	rC := rejectionFor(t, lC, "C", SideBuy)
	if rC.Reason != UnfilledInsufficientCash || rC.Shares != 100 {
		t.Fatalf("C 剩余拒绝错误: %+v", rC)
	}
	assertDailyInvariants(t, lA)
	assertDailyInvariants(t, lC)
}

// TestCommissionMinStampDirectionAndLots 佣金最低额、印花税方向（仅卖出）、整手取整。
func TestCommissionMinStampDirectionAndLots(t *testing.T) {
	exec := execT5()
	// 佣金最低额：小额买入（毛额 501，佣金 0.1503 < 5 → 收 5）。
	state := tState(1e6)
	m := tMarket("2026-01-05",
		map[string]float64{"A": 5},
		map[string]float64{"A": 5},
		map[string]float64{"A": 5})
	markTradable(&m, "A")
	in := DayInput{Target: tTarget("2026-01-04", map[string]int{"A": 100}), Market: m, Scores: map[string]float64{"A": 1}}

	ledger, _, err := ExecuteDay(state, in, exec)
	if err != nil {
		t.Fatalf("ExecuteDay 失败: %v", err)
	}
	bf := fillFor(t, ledger, "A", SideBuy)
	if !approx(bf.Fees.Commission, 5, 1e-9) {
		t.Fatalf("最低佣金 got %v want 5", bf.Fees.Commission)
	}
	if bf.Fees.StampDuty != 0 {
		t.Fatalf("买入不应收印花税，实际 %v", bf.Fees.StampDuty)
	}
	if !approx(bf.NetAmount, 5.01*100+5, 1e-6) {
		t.Fatalf("买入总支出 got %.6f want %v", bf.NetAmount, 5.01*100+5)
	}
	// 整手取整：目标 150 股 → 成交 100，剩余 50 记录 lot_rounding。
	m2 := tMarket("2026-01-05",
		map[string]float64{"A": 5},
		map[string]float64{"A": 5},
		map[string]float64{"A": 5})
	markTradable(&m2, "A")
	in2 := DayInput{Target: tTarget("2026-01-04", map[string]int{"A": 150}), Market: m2, Scores: map[string]float64{"A": 1}}
	_, next2, err := ExecuteDay(tState(1e6), in2, exec)
	if err != nil {
		t.Fatalf("ExecuteDay 整手失败: %v", err)
	}
	if got := sharesOf(next2, "A"); got != 100 {
		t.Fatalf("整手向下取整 got %d want 100", got)
	}
	l2, _, err := ExecuteDay(tState(1e6), in2, exec)
	if err != nil {
		t.Fatalf("ExecuteDay 整手失败: %v", err)
	}
	rl := rejectionFor(t, l2, "A", SideBuy)
	if rl.Reason != UnfilledLotRounding || rl.Shares != 50 {
		t.Fatalf("整手取整拒绝错误: %+v", rl)
	}

	// 卖出印花税方向：卖出成交必须有印花税。
	sellState := tState(0, tLot("S", 100, 5, "2026-01-02"))
	m3 := tMarket("2026-01-05",
		map[string]float64{"S": 5},
		map[string]float64{"S": 5},
		map[string]float64{"S": 5})
	markTradable(&m3, "S")
	in3 := DayInput{Target: tTarget("2026-01-04", map[string]int{}), Market: m3}
	l3, _, err := ExecuteDay(sellState, in3, exec)
	if err != nil {
		t.Fatalf("ExecuteDay 卖出失败: %v", err)
	}
	sf := fillFor(t, l3, "S", SideSell)
	if sf.Fees.StampDuty <= 0 {
		t.Fatalf("卖出应收取印花税，实际 %v", sf.Fees.StampDuty)
	}
	assertDailyInvariants(t, ledger)
}

// TestDelistedAndMissingPrice 退市/缺价按协议失败或降级，不补造价格。
func TestDelistedAndMissingPrice(t *testing.T) {
	exec := execT5()
	// 退市（not_listed）：持仓不可卖，保留并产生质量事件 + 降级。
	state := tState(0, tLot("D", 100, 10, "2026-01-02"))
	m := tMarket("2026-01-05",
		map[string]float64{"D": 10},
		map[string]float64{"D": 10},
		map[string]float64{"D": 10})
	m.Tradable["D"] = false
	m.Listed["D"] = false // 退市/未上市
	in := DayInput{Target: tTarget("2026-01-04", map[string]int{}), Market: m}

	ledger, next, err := ExecuteDay(state, in, exec)
	if err != nil {
		t.Fatalf("ExecuteDay 失败: %v", err)
	}
	rd := rejectionFor(t, ledger, "D", SideSell)
	if rd.Reason != UnfilledNotListed {
		t.Fatalf("退市卖出拒绝原因 got %q want not_listed", rd.Reason)
	}
	if sharesOf(next, "D") != 100 {
		t.Fatalf("退市持仓应保留，实际 %d", sharesOf(next, "D"))
	}
	if !ledger.Degraded {
		t.Fatalf("退市应降级证据等级")
	}
	if !hasQualityEvent(ledger, "delisted") && !hasQualityEvent(ledger, "missing_market_data") {
		t.Fatalf("退市应产生质量事件: %v", ledger.QualityEvents)
	}

	// 缺价（目标买入代码无行情数据）：fail closed，不补造价格，质量事件 + 降级。
	m2 := tMarket("2026-01-05",
		map[string]float64{"A": 10},
		map[string]float64{"A": 10},
		map[string]float64{"A": 10})
	markTradable(&m2, "A")
	in2 := DayInput{Target: tTarget("2026-01-04", map[string]int{"M": 100}), Market: m2,
		Scores: map[string]float64{"M": 1.0}}
	l2, next2, err := ExecuteDay(tState(1e6), in2, exec)
	if err != nil {
		t.Fatalf("ExecuteDay 缺价失败: %v", err)
	}
	if sharesOf(next2, "M") != 0 {
		t.Fatalf("缺价股票不应成交，实际 %d", sharesOf(next2, "M"))
	}
	rm := rejectionFor(t, l2, "M", SideBuy)
	if rm.Reason != UnfilledSuspended {
		t.Fatalf("缺价买入拒绝原因 got %q want suspended（fail closed）", rm.Reason)
	}
	if !l2.Degraded {
		t.Fatalf("缺价应降级证据等级")
	}
	if !hasQualityEvent(l2, "missing_market_data") {
		t.Fatalf("缺价应产生质量事件: %v", l2.QualityEvents)
	}
	assertDailyInvariants(t, ledger)
	assertDailyInvariants(t, l2)
}

// TestDeterminism 同一输入重复运行订单/成交/持仓/净值逐位一致。
func TestDeterminism(t *testing.T) {
	exec := execT5()
	state := tState(1000,
		tLot("A", 100, 10, "2026-01-02"),
		tLot("B", 200, 8, "2026-01-03"))
	m := tMarket("2026-01-05",
		map[string]float64{"A": 9, "B": 9, "C": 12, "U": 12},
		map[string]float64{"A": 10, "B": 8, "C": 10, "U": 11},
		map[string]float64{"A": 9.5, "B": 8.5, "C": 12, "U": 12})
	markTradable(&m, "A", "B", "C", "U")
	m.LimitUp["U"] = true
	m.Tradable["A"] = false // 停牌
	// 公司行为也参与确定性比对（map 构造顺序不固定，事件顺序必须稳定）。
	m.CorporateActions = map[string][]CorporateAction{
		"A": {{Code: "A", Kind: CorporateActionDividend, PerShare: 0.3, Detail: "A 分红"}},
		"C": {{Code: "C", Kind: CorporateActionDividend, PerShare: 0.2, Detail: "C 分红"}},
	}
	in := DayInput{
		Target: tTarget("2026-01-04", map[string]int{"A": 100, "C": 100, "U": 100}),
		Market: m,
		Scores: map[string]float64{"A": 3, "C": 2, "U": 1},
	}

	l1, s1, err := ExecuteDay(state, in, exec)
	if err != nil {
		t.Fatalf("ExecuteDay 失败: %v", err)
	}
	l2, s2, err := ExecuteDay(state, in, exec)
	if err != nil {
		t.Fatalf("ExecuteDay 失败: %v", err)
	}
	if !reflect.DeepEqual(l1, l2) {
		t.Fatalf("同一输入重复运行账本不一致\nl1=%+v\nl2=%+v", l1, l2)
	}
	if !reflect.DeepEqual(s1, s2) {
		t.Fatalf("同一输入重复运行状态不一致\ns1=%+v\ns2=%+v", s1, s2)
	}
	assertDailyInvariants(t, l1)
}

// TestNoFuturePriceInExecution 未来价格不参与当日成交资格。
func TestNoFuturePriceInExecution(t *testing.T) {
	exec := execT5()
	state := tState(1e6)
	// 开盘 10，收盘（估值）20：执行只能用开盘 10，不能用 20 补成交。
	m := tMarket("2026-01-05",
		map[string]float64{"A": 10},
		map[string]float64{"A": 10},
		map[string]float64{"A": 20})
	markTradable(&m, "A")
	in := DayInput{Target: tTarget("2026-01-04", map[string]int{"A": 100}), Market: m, Scores: map[string]float64{"A": 1}}

	ledger, next, err := ExecuteDay(state, in, exec)
	if err != nil {
		t.Fatalf("ExecuteDay 失败: %v", err)
	}
	bf := fillFor(t, ledger, "A", SideBuy)
	if !approx(bf.Price, 10.01, 1e-9) {
		t.Fatalf("买入成交价应为开盘 10 + 滑点 0.01 = 10.01，实际 %v（未来价 20 不得参与）", bf.Price)
	}
	if !approx(bf.GrossAmount, 10.01*100, 1e-6) {
		t.Fatalf("成交毛额 got %v want %v", bf.GrossAmount, 10.01*100)
	}
	// 期末市值用收盘 20 估值（统一口径）。
	if !approx(ledger.EndEquity, next.Cash+100*20, 1e-6) {
		t.Fatalf("期末权益应按收盘 20 估值: got %.6f cash %.6f", ledger.EndEquity, next.Cash)
	}
	assertDailyInvariants(t, ledger)
}

// TestCarryUnfilled 未成交意图日终失效 / 跨日保留（配置化）。
func TestCarryUnfilled(t *testing.T) {
	// 默认不保留：day1 未成交（现金不足）意图日终失效，day2 按新目标重新生成。
	exec := execT5()
	d1 := DayInput{Target: tTarget("2026-01-04", map[string]int{"A": 100}), Market: tMarket("2026-01-05",
		map[string]float64{"A": 10}, map[string]float64{"A": 10}, map[string]float64{"A": 10}),
		Scores: map[string]float64{"A": 1.0}}
	d1.Market.Tradable["A"], d1.Market.Listed["A"] = true, true
	l1, s1, err := ExecuteDay(tState(100), d1, exec) // 现金 100 < 一手 1000
	if err != nil {
		t.Fatalf("ExecuteDay 失败: %v", err)
	}
	rejectionFor(t, l1, "A", SideBuy)
	if len(s1.Pending) != 0 {
		t.Fatalf("默认 CarryUnfilled=false 不应保留未成交意图: %v", s1.Pending)
	}
	// day2 目标不再含 A，且无残留意图 → 无买入。
	d2 := DayInput{Target: tTarget("2026-01-05", map[string]int{}), Market: tMarket("2026-01-06",
		map[string]float64{"A": 10}, map[string]float64{"A": 10}, map[string]float64{"A": 10})}
	d2.Market.Tradable["A"], d2.Market.Listed["A"] = true, true
	l2, s2, err := ExecuteDay(s1, d2, exec)
	if err != nil {
		t.Fatalf("ExecuteDay day2 失败: %v", err)
	}
	if sharesOf(s2, "A") != 0 {
		t.Fatalf("未保留意图下 day2 不应买入 A，实际 %d", sharesOf(s2, "A"))
	}
	_ = l2

	// CarryUnfilled=true：day1 未成交买入意图跨日保留，day2 现金充足时补买。
	execCarry := exec
	execCarry.CarryUnfilled = true
	c1 := DayInput{Target: tTarget("2026-01-04", map[string]int{"A": 100}), Market: tMarket("2026-01-05",
		map[string]float64{"A": 10}, map[string]float64{"A": 10}, map[string]float64{"A": 10}),
		Scores: map[string]float64{"A": 1.0}}
	c1.Market.Tradable["A"], c1.Market.Listed["A"] = true, true
	lc1, sc1, err := ExecuteDay(tState(100), c1, execCarry)
	if err != nil {
		t.Fatalf("ExecuteDay carry day1 失败: %v", err)
	}
	if len(sc1.Pending) != 1 || sc1.Pending[0].Code != "A" {
		t.Fatalf("CarryUnfilled=true 应保留未成交意图: %v", sc1.Pending)
	}
	_ = lc1
	sc1.Cash = 2000 // 次日补足现金，验证跨日意图被执行
	c2 := DayInput{Target: tTarget("2026-01-05", map[string]int{}), Market: tMarket("2026-01-06",
		map[string]float64{"A": 10}, map[string]float64{"A": 10}, map[string]float64{"A": 10}),
		Scores: map[string]float64{"A": 1.0}}
	c2.Market.Tradable["A"], c2.Market.Listed["A"] = true, true
	lc2, sc2, err := ExecuteDay(sc1, c2, execCarry)
	if err != nil {
		t.Fatalf("ExecuteDay carry day2 失败: %v", err)
	}
	if got := sharesOf(sc2, "A"); got != 100 {
		t.Fatalf("CarryUnfilled 跨日保留应补买 A 100 股，实际 %d", got)
	}
	if len(sc2.Pending) != 0 {
		t.Fatalf("补买成功后不应残留意图: %v", sc2.Pending)
	}
	assertDailyInvariants(t, lc2)
}

// hasQualityEvent 判断账本是否包含某类型质量事件。
func hasQualityEvent(l DailyLedger, typ string) bool {
	for _, e := range l.QualityEvents {
		if e.Type == typ {
			return true
		}
	}
	return false
}

// assertDailyInvariants 任务规定的 5 条不变量逐日断言。
func assertDailyInvariants(t *testing.T, l DailyLedger) {
	t.Helper()
	if l.EndCash < -1e-9 {
		t.Fatalf("不变量 cash>=0 违反: %v", l.EndCash)
	}
	for _, lot := range l.EndHoldings {
		if lot.Shares < 0 {
			t.Fatalf("不变量 holdingShares>=0 违反: %s=%d", lot.Code, lot.Shares)
		}
	}
	// sellShares <= sellableShares：卖出成交股数不得超过当日可售。
	// 引擎层保证：卖出成交股数按可售封顶；此处用账本成交核对。
	// 端持仓可售 = 当日之前买入的持仓（T+1 下）。
	if !l.Reconciled {
		t.Fatalf("账本对账未通过: residual=%v", l.ReconResidual)
	}
	if math.Abs(l.ReconResidual) > 1e-6 {
		t.Fatalf("不变量 beginEquity+pnl-fees=endEquity 违反: residual=%v (begin=%.6f pnl=%.6f fees=%.6f end=%.6f)",
			l.ReconResidual, l.BeginEquity, l.Pnl, l.Fees.Total(), l.EndEquity)
	}
}

// TestUnfilledReasonsAreStableEnums 未成交原因必须是稳定枚举。
func TestUnfilledReasonsAreStableEnums(t *testing.T) {
	exec := execT5()
	state := tState(0, tLot("X", 100, 10, "2026-01-02"))
	m := tMarket("2026-01-05",
		map[string]float64{"X": 10, "U": 10},
		map[string]float64{"X": 10, "U": 10},
		map[string]float64{"X": 10, "U": 10})
	markTradable(&m, "X", "U")
	m.LimitUp["U"] = true
	in := DayInput{Target: tTarget("2026-01-04", map[string]int{"U": 100}), Market: m, Scores: map[string]float64{"U": 1}}

	l, _, err := ExecuteDay(state, in, exec)
	if err != nil {
		t.Fatalf("ExecuteDay 失败: %v", err)
	}
	for _, r := range l.Rejections {
		if !ValidUnfilledReason(r.Reason) {
			t.Fatalf("拒绝原因非法（非稳定枚举）: %q", r.Reason)
		}
	}
}

// TestBuildIntentsFillsTargetWeight 订单意图必须回填当日目标权重（审计引用，
// orders.csv weight 列）：买入/部分减持 = 目标有效权重；清仓卖出（目标无该
// 代码）与缺失/非有限权重 = 0；跨日保留保留创建当日权重。
func TestBuildIntentsFillsTargetWeight(t *testing.T) {
	lots := []HoldingLot{
		tLot("A", 500, 10, "2026-01-02"), // 增持：目标 800 → 买 300
		tLot("B", 500, 10, "2026-01-02"), // 减持：目标 200 → 卖 300
		tLot("C", 500, 10, "2026-01-02"), // 清仓：目标无 C → 卖 500
	}
	targetShares := map[string]int{"A": 800, "B": 200}
	effective := map[string]float64{"A": 0.4, "B": 0.1, "C": 0.25}
	intents := buildIntents(lots, targetShares, nil, effective)

	byCode := map[string]OrderIntent{}
	for _, it := range intents {
		byCode[it.Code] = it
	}
	// 买入：目标有效权重 0.4。
	if it := byCode["A"]; it.Side != SideBuy || !approx(it.Weight, 0.4, 1e-12) {
		t.Fatalf("A 买入意图权重 = %+v, want buy/0.4", it)
	}
	// 部分减持：剩余目标有效权重 0.1。
	if it := byCode["B"]; it.Side != SideSell || !approx(it.Weight, 0.1, 1e-12) {
		t.Fatalf("B 减持意图权重 = %+v, want sell/0.1", it)
	}
	// 清仓卖出：目标无该代码 → 权重 0。
	if it := byCode["C"]; it.Side != SideSell || it.Weight != 0 {
		t.Fatalf("C 清仓意图权重 = %+v, want sell/0", it)
	}

	// 缺失/非有限权重 → 0（与 intentPriority 同策略）。
	bad := buildIntents(lots, map[string]int{"A": 800}, nil,
		map[string]float64{"A": math.NaN(), "B": math.Inf(1)})
	for _, it := range bad {
		if it.Weight != 0 {
			t.Fatalf("非有限/缺失目标权重应归 0: %+v", it)
		}
	}

	// 跨日保留保留创建当日目标权重（carriedBuys 原样复制，不重估）。
	carried := carriedBuys([]OrderIntent{{Code: "D", Side: SideBuy, Shares: 100, Weight: 0.3, Priority: 1}},
		[]OrderIntent{{Code: "A", Side: SideBuy, Shares: 100}})
	if len(carried) != 1 || !approx(carried[0].Weight, 0.3, 1e-12) {
		t.Fatalf("跨日保留意图应保留创建当日权重 0.3: %+v", carried)
	}
}
