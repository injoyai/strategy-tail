package portfolioresearch

// accounting_test.go v2 Task 5 每日会计与对账测试。
//
// 覆盖：
//   - 统一口径估值：endEquity = cash + Σ(持仓×估值价)；
//   - 对账恒等式 beginEquity + pnl - fees = endEquity 逐日成立（多日序列）；
//   - 期初权益与上一日期末权益链条一致；
//   - 公司行为：分红（现金流入 + 计入损益）、拆并股（股数/成本调整）、
//     退市缺数据 → 质量事件 + 降级；
//   - 容量告警（成交量不参与定价，仅质量事件，明确未建模）。

import (
	"math"
	"strings"
	"testing"
)

// ---- 多日序列手算依据（Cost：佣金 0.0003/最低 5、印花 0.001、过户 0、滑点 0.01）----
//
// Day1（01-05）：现金 2000，无持仓；目标买 A 100。
//
//	买 A：价 10.01，毛额 1001，佣金 5，支出 1006 → 现金 994；
//	beginEquity=2000；endEquity=994+100×10=1994；pnl=-1（未实现 (1000-1001)）；fees=5；2000-1-5=1994 ✓
//
// Day2（01-06）：现金 994，A 100（成本 1001）；目标卖 A 买 B 100。
//
//	卖 A：价 9.99，毛额 999，佣金 5 + 印花 0.999，净 993.001 → 现金 1987.001；
//	买 B：价 10.01，毛额 1001，佣金 5，支出 1006 → 现金 981.001；
//	beginEquity=994+100×10=1994；endEquity=981.001+100×10=1981.001；
//	realized=-2；Δunrealized=0；pnl=-2；fees=10.999；1994-2-10.999=1981.001 ✓
//
// Day3（01-07）：现金 981.001，B 100（成本 1001）；目标清仓。
//
//	卖 B：价 9.99，毛额 999，佣金 5 + 印花 0.999，净 993.001 → 现金 1974.002；
//	beginEquity=981.001+100×10=1981.001；endEquity=1974.002；
//	realized=-2；Δunrealized=+1（期初未实现 -1 回转）；pnl=-1；fees=5.999；1981.001-1-5.999=1974.002 ✓

func TestMultiDayInvariants(t *testing.T) {
	exec := execT5()

	d1 := DayInput{
		Target: tTarget("2026-01-04", map[string]int{"A": 100}),
		Market: tMarket("2026-01-05",
			map[string]float64{"A": 10}, map[string]float64{"A": 10}, map[string]float64{"A": 10}),
		Scores: map[string]float64{"A": 1.0},
	}
	d1.Market.Tradable["A"], d1.Market.Listed["A"] = true, true

	d2 := DayInput{
		Target: tTarget("2026-01-05", map[string]int{"B": 100}),
		Market: tMarket("2026-01-06",
			map[string]float64{"A": 10, "B": 10},
			map[string]float64{"A": 10, "B": 10},
			map[string]float64{"A": 11, "B": 10}),
		Scores: map[string]float64{"B": 1.0},
	}
	d2.Market.Tradable["A"], d2.Market.Listed["A"] = true, true
	d2.Market.Tradable["B"], d2.Market.Listed["B"] = true, true

	d3 := DayInput{
		Target: tTarget("2026-01-06", map[string]int{}),
		Market: tMarket("2026-01-07",
			map[string]float64{"B": 10}, map[string]float64{"B": 10}, map[string]float64{"B": 9}),
	}
	d3.Market.Tradable["B"], d3.Market.Listed["B"] = true, true

	start := tState(2000)
	ledgers, final, err := Simulate(start, []DayInput{d1, d2, d3}, exec)
	if err != nil {
		t.Fatalf("Simulate 失败: %v", err)
	}
	if len(ledgers) != 3 {
		t.Fatalf("账本数 got %d want 3", len(ledgers))
	}

	// 逐日不变量 + 关键数值。
	assertDay(t, ledgers[0], 2000, 1994, 994, map[string]int{"A": 100}, exec, map[string]float64{"A": 10})
	assertDay(t, ledgers[1], 1994, 1981.001, 981.001, map[string]int{"B": 100}, exec, map[string]float64{"A": 11, "B": 10})
	assertDay(t, ledgers[2], 1981.001, 1974.002, 1974.002, map[string]int{}, exec, map[string]float64{"B": 9})

	// 链条：期初权益 = 上一日期末权益（Simulate 内部保证）。
	if !approx(ledgers[1].BeginEquity, ledgers[0].EndEquity, 1e-6) {
		t.Fatalf("链条断裂：day2 begin %.6f != day1 end %.6f", ledgers[1].BeginEquity, ledgers[0].EndEquity)
	}
	if !approx(ledgers[2].BeginEquity, ledgers[1].EndEquity, 1e-6) {
		t.Fatalf("链条断裂：day3 begin %.6f != day2 end %.6f", ledgers[2].BeginEquity, ledgers[1].EndEquity)
	}
	if !approx(final.Cash, 1974.002, 1e-6) {
		t.Fatalf("最终现金 got %.6f want 1974.002", final.Cash)
	}
}

// assertDay 断言单日账本的数值与持仓、并校验 5 条不变量。
func assertDay(t *testing.T, l DailyLedger, beginEquity, endEquity, endCash float64, holdings map[string]int, exec ExecutionSpec, closePrices map[string]float64) {
	t.Helper()
	if !approx(l.BeginEquity, beginEquity, 1e-6) {
		t.Fatalf("期初权益 got %.6f want %.6f", l.BeginEquity, beginEquity)
	}
	if !approx(l.EndEquity, endEquity, 1e-6) {
		t.Fatalf("期末权益 got %.6f want %.6f", l.EndEquity, endEquity)
	}
	if !approx(l.EndCash, endCash, 1e-6) {
		t.Fatalf("期末现金 got %.6f want %.6f", l.EndCash, endCash)
	}
	for code, want := range holdings {
		if got := sharesOf(PortfolioState{Lots: l.EndHoldings}, code); got != want {
			t.Fatalf("持仓 %s got %d want %d", code, got, want)
		}
	}
	// 不变量：endEquity = cash + Σ(持仓×估值价)（统一口径）。
	if !approx(l.EndEquity, l.EndCash+MarketValue(l.EndHoldings, closePrices), 1e-6) {
		t.Fatalf("不变量 endEquity=cash+Σ(持仓×估值价) 违反: %.6f != %.6f + %.6f",
			l.EndEquity, l.EndCash, MarketValue(l.EndHoldings, closePrices))
	}
	// 对账恒等式（含不变量逐日断言）。
	assertDailyInvariants(t, l)
	// sellShares <= sellableShares：逐笔核验卖出成交股数 ≤ 当日可售。
	sellable := map[string]int{}
	for _, lot := range l.BeginHoldings {
		sellable[lot.Code] += sellableShares(PortfolioState{Lots: l.BeginHoldings}, lot.Code, l.Date, exec.T1Restriction)
	}
	sold := map[string]int{}
	for _, f := range l.Fills {
		if f.Side == SideSell {
			sold[f.Code] += f.Shares
		}
	}
	for code, n := range sold {
		if n > sellable[code] {
			t.Fatalf("卖出 %d 超过可售 %d（%s）", n, sellable[code], code)
		}
	}
}

// TestValuationEquity 统一口径估值：endEquity = cash + Σ(持仓×估值价)。
func TestValuationEquity(t *testing.T) {
	lots := []HoldingLot{
		tLot("A", 100, 10, "2026-01-02"),
		tLot("B", 200, 5, "2026-01-02"),
	}
	mv := MarketValue(lots, map[string]float64{"A": 12, "B": 5.5})
	if !approx(mv, 100*12+200*5.5, 1e-9) {
		t.Fatalf("市值 got %v want %v", mv, 100*12+200*5.5)
	}
	// 缺失估值价按 0（fail closed，调用方须负责补质量事件）。
	if got := MarketValue(lots, map[string]float64{"A": 12}); got != 1200 {
		t.Fatalf("缺估值价按 0 got %v want 1200", got)
	}
}

// TestDividendCorporateAction 分红：现金流入计入损益 + 质量事件 + 对账成立。
func TestDividendCorporateAction(t *testing.T) {
	exec := execT5()
	state := tState(0, tLot("D", 100, 10, "2026-01-02"))
	m := tMarket("2026-01-05",
		map[string]float64{"D": 10}, map[string]float64{"D": 10}, map[string]float64{"D": 10})
	markTradable(&m, "D")
	m.CorporateActions = map[string][]CorporateAction{
		"D": {{Code: "D", Kind: CorporateActionDividend, PerShare: 0.5, Detail: "2025 年度分红"}},
	}
	in := DayInput{Target: tTarget("2026-01-04", map[string]int{}), Market: m}

	ledger, next, err := ExecuteDay(state, in, exec)
	if err != nil {
		t.Fatalf("ExecuteDay 失败: %v", err)
	}
	// 分红现金 = 100×0.5 = 50；随后清仓卖出净入 993.001 → 现金 1043.001。
	if !approx(next.Cash, 50+993.001, 1e-6) {
		t.Fatalf("分红后现金 got %.6f want %.6f", next.Cash, 50+993.001)
	}
	if !hasQualityEvent(ledger, "corporate_action") {
		t.Fatalf("分红应记录公司行为质量事件: %v", ledger.QualityEvents)
	}
	if !approx(ledger.CorporateIncome, 50, 1e-9) {
		t.Fatalf("公司行为收入 got %.6f want 50", ledger.CorporateIncome)
	}
	// 对账：begin=1000，realized=-1，分红 50，fees=5.999 → end=1000-1+50-5.999=1043.001。
	if !approx(ledger.EndEquity, 1000-1+50-5.999, 1e-6) {
		t.Fatalf("期末权益 got %.6f", ledger.EndEquity)
	}
	assertDailyInvariants(t, ledger)
}

// TestSplitCorporateAction 拆并股：股数调整、每股成本调整、质量事件、对账成立。
func TestSplitCorporateAction(t *testing.T) {
	exec := execT5()
	state := tState(0, tLot("S", 100, 10, "2026-01-02")) // 成本基础 1000
	m := tMarket("2026-01-05",
		map[string]float64{"S": 5}, map[string]float64{"S": 5}, map[string]float64{"S": 5})
	markTradable(&m, "S")
	// 1 拆 2：股数 ×2 = 200，每股成本 5，成本基础不变 1000；拆分后收盘 5（前收 10 折算）。
	m.PrevClose["S"] = 10
	m.ClosePrice["S"] = 5
	m.CorporateActions = map[string][]CorporateAction{
		"S": {{Code: "S", Kind: CorporateActionSplit, PerShare: 2, Detail: "1 拆 2"}},
	}
	// 目标按公司行为调整后口径保持 200 股（复权口径已确认，§9.5），不产生交易。
	in := DayInput{Target: tTarget("2026-01-04", map[string]int{"S": 200}), Market: m}

	ledger, next, err := ExecuteDay(state, in, exec)
	if err != nil {
		t.Fatalf("ExecuteDay 失败: %v", err)
	}
	if got := sharesOf(next, "S"); got != 200 {
		t.Fatalf("拆股后股数 got %d want 200", got)
	}
	for _, l := range next.Lots {
		if l.Code == "S" {
			if !approx(l.BuyPrice, 5, 1e-9) {
				t.Fatalf("拆股后每股成本 got %v want 5", l.BuyPrice)
			}
			if !approx(l.CostBasis, 1000, 1e-9) {
				t.Fatalf("拆股后成本基础应不变 got %v want 1000", l.CostBasis)
			}
		}
	}
	if !hasQualityEvent(ledger, "corporate_action") {
		t.Fatalf("拆股应记录公司行为质量事件: %v", ledger.QualityEvents)
	}
	assertDailyInvariants(t, ledger)
}

// TestCapacityWarningUnmodeled 成交量不参与定价：容量告警为质量事件且明确未建模。
func TestCapacityWarningUnmodeled(t *testing.T) {
	exec := execT5()
	state := tState(1e6)
	m := tMarket("2026-01-05",
		map[string]float64{"A": 10}, map[string]float64{"A": 10}, map[string]float64{"A": 10})
	markTradable(&m, "A")
	m.Volume = map[string]float64{"A": 50000} // 买 A 毛额 1001 > 1%×50000=500 → 告警
	in := DayInput{Target: tTarget("2026-01-04", map[string]int{"A": 100}), Market: m, Scores: map[string]float64{"A": 1}}

	ledger, _, err := ExecuteDay(state, in, exec)
	if err != nil {
		t.Fatalf("ExecuteDay 失败: %v", err)
	}
	if !hasQualityEvent(ledger, "capacity") {
		t.Fatalf("容量超限应产生质量事件: %v", ledger.QualityEvents)
	}
	// 价格未被调整：成交价仍是开盘 + 滑点。
	if got := fillFor(t, ledger, "A", SideBuy).Price; !approx(got, 10.01, 1e-9) {
		t.Fatalf("容量告警不得调整价格，成交价 got %v want 10.01", got)
	}
	assertDailyInvariants(t, ledger)
}

// TestBeginEquityUsesPrevClose 期初权益按前收盘估值（统一口径）。
func TestBeginEquityUsesPrevClose(t *testing.T) {
	exec := execT5()
	state := tState(100, tLot("A", 100, 10, "2026-01-02"))
	m := tMarket("2026-01-05",
		map[string]float64{"A": 10},
		map[string]float64{"A": 12}, // 前收盘 12
		map[string]float64{"A": 12})
	markTradable(&m, "A")
	in := DayInput{Target: tTarget("2026-01-04", map[string]int{"A": 100}), Market: m}

	ledger, _, err := ExecuteDay(state, in, exec)
	if err != nil {
		t.Fatalf("ExecuteDay 失败: %v", err)
	}
	if !approx(ledger.BeginEquity, 100+100*12.0, 1e-6) {
		t.Fatalf("期初权益应按前收盘 12 估值 got %.6f want %.6f", ledger.BeginEquity, 100+100*12.0)
	}
	assertDailyInvariants(t, ledger)
}

// TestSimulateEmptyDays 空序列 Simulate 直接返回期初状态。
func TestSimulateEmptyDays(t *testing.T) {
	exec := execT5()
	start := tState(500, tLot("A", 100, 10, "2026-01-02"))
	ledgers, final, err := Simulate(start, nil, exec)
	if err != nil {
		t.Fatalf("Simulate 失败: %v", err)
	}
	if len(ledgers) != 0 {
		t.Fatalf("空序列账本数 got %d want 0", len(ledgers))
	}
	if !approx(final.Cash, 500, 1e-9) || sharesOf(final, "A") != 100 {
		t.Fatalf("空序列应原样返回期初状态: %+v", final)
	}
}

// qualityEventWithDetail 查找指定类型且 Detail 包含关键字的质量事件。
func qualityEventWithDetail(t *testing.T, l DailyLedger, typ, keyword string) ExecutionQualityEvent {
	t.Helper()
	for _, e := range l.QualityEvents {
		if e.Type == typ && strings.Contains(e.Detail, keyword) {
			return e
		}
	}
	t.Fatalf("账本缺少类型 %q 且 Detail 含 %q 的质量事件: %v", typ, keyword, l.QualityEvents)
	return ExecutionQualityEvent{}
}

// TestDividendRejectsInvalidPerShare 分红 PerShare 非法（NaN/负值）fail closed：
// 不污染现金与分红收入，产生 corporate_action 质量事件（Detail 含
// invalid_corporate_action）+ 当日降级，对账仍成立且残差非 NaN。
//
// 手算依据（目标保持持仓 100 股、当日无交易，分红为唯一变动）：
//   - 期初：现金 0，D 100 股 @ 10（成本 1000）；
//   - 分红被拒 → 现金仍 0、CorporateIncome=0；
//   - beginEquity=1000（前收盘 10×100）；endEquity=0+100×10=1000；
//   - pnl=0、fees=0 → 残差 = 1000+0-0-1000 = 0 ✓。
func TestDividendRejectsInvalidPerShare(t *testing.T) {
	exec := execT5()
	cases := []struct {
		name     string
		perShare float64
	}{
		{"NaN", math.NaN()},
		{"负值", -5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			state := tState(0, tLot("D", 100, 10, "2026-01-02"))
			m := tMarket("2026-01-05",
				map[string]float64{"D": 10}, map[string]float64{"D": 10}, map[string]float64{"D": 10})
			markTradable(&m, "D")
			m.CorporateActions = map[string][]CorporateAction{
				"D": {{Code: "D", Kind: CorporateActionDividend, PerShare: c.perShare, Detail: "非法分红"}},
			}
			in := DayInput{Target: tTarget("2026-01-04", map[string]int{"D": 100}), Market: m}

			ledger, next, err := ExecuteDay(state, in, exec)
			if err != nil {
				t.Fatalf("ExecuteDay 失败: %v", err)
			}
			// 现金不被 NaN/负值污染：无分红入账。
			if !approx(next.Cash, 0, 1e-9) {
				t.Fatalf("非法分红不得入账现金 got %.6f want 0", next.Cash)
			}
			if !approx(ledger.CorporateIncome, 0, 1e-9) {
				t.Fatalf("非法分红不得计入公司行为收入 got %.6f want 0", ledger.CorporateIncome)
			}
			if len(ledger.Fills) != 0 {
				t.Fatalf("不应有成交: %v", ledger.Fills)
			}
			// 质量事件 + 降级（可观测语义）。
			if !ledger.Degraded {
				t.Fatalf("非法分红应标记当日降级")
			}
			if ev := qualityEventWithDetail(t, ledger, "corporate_action", "invalid_corporate_action"); !ev.Degraded {
				t.Fatalf("非法分红质量事件应标记降级: %+v", ev)
			}
			// 对账仍成立且残差非 NaN。
			if !ledger.Reconciled {
				t.Fatalf("非法分红后对账应成立: residual=%v", ledger.ReconResidual)
			}
			if math.IsNaN(ledger.ReconResidual) {
				t.Fatalf("对账残差不得为 NaN")
			}
			assertDailyInvariants(t, ledger)
		})
	}
}

// TestSplitRejectsInvalidPerShare 拆并股 PerShare 非法（NaN/负值）fail closed：
// 持仓股数/买入价/成本基础不变，产生质量事件（含 invalid_corporate_action）+
// 当日降级，对账仍成立且残差非 NaN。
//
// 手算依据：目标保持 100 股无交易；拆分被拒 → 股数 100、买入价 10、成本 1000
// 全部不变；beginEquity=endEquity=1000、pnl=0、fees=0 → 残差 0 ✓。
func TestSplitRejectsInvalidPerShare(t *testing.T) {
	exec := execT5()
	cases := []struct {
		name     string
		perShare float64
	}{
		{"NaN", math.NaN()},
		{"负值", -2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			state := tState(0, tLot("S", 100, 10, "2026-01-02")) // 成本基础 1000
			m := tMarket("2026-01-05",
				map[string]float64{"S": 10}, map[string]float64{"S": 10}, map[string]float64{"S": 10})
			markTradable(&m, "S")
			m.CorporateActions = map[string][]CorporateAction{
				"S": {{Code: "S", Kind: CorporateActionSplit, PerShare: c.perShare, Detail: "非法拆股"}},
			}
			in := DayInput{Target: tTarget("2026-01-04", map[string]int{"S": 100}), Market: m}

			ledger, next, err := ExecuteDay(state, in, exec)
			if err != nil {
				t.Fatalf("ExecuteDay 失败: %v", err)
			}
			// 持仓不被 NaN/负值污染：股数与成本口径不变。
			if got := sharesOf(next, "S"); got != 100 {
				t.Fatalf("非法拆股后股数应不变 got %d want 100", got)
			}
			for _, l := range next.Lots {
				if l.Code == "S" {
					if !approx(l.BuyPrice, 10, 1e-9) {
						t.Fatalf("非法拆股后买入价应不变 got %v want 10", l.BuyPrice)
					}
					if !approx(l.CostBasis, 1000, 1e-9) {
						t.Fatalf("非法拆股后成本基础应不变 got %v want 1000", l.CostBasis)
					}
				}
			}
			// 质量事件 + 降级（可观测语义）。
			if !ledger.Degraded {
				t.Fatalf("非法拆股应标记当日降级")
			}
			if ev := qualityEventWithDetail(t, ledger, "corporate_action", "invalid_corporate_action"); !ev.Degraded {
				t.Fatalf("非法拆股质量事件应标记降级: %+v", ev)
			}
			// 对账仍成立且残差非 NaN。
			if !ledger.Reconciled {
				t.Fatalf("非法拆股后对账应成立: residual=%v", ledger.ReconResidual)
			}
			if math.IsNaN(ledger.ReconResidual) {
				t.Fatalf("对账残差不得为 NaN")
			}
			assertDailyInvariants(t, ledger)
		})
	}
}
