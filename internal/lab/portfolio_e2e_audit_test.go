package lab

// portfolio_e2e_audit_test.go v2 Task 11 端到端固定案例审计（计划 Task 11 §端到端）。
//
// 审计目标：
//  1. 金标准固定案例：两个已验证因子 × 6 只股票 × 单信号日/执行日，逐项手算
//     变换（rank/z-score）→ 分数（合成）→ 目标权重（含整手/现金）→ 成交
//     （价格/数量/费用）→ 现金 → 净值 → 归因贡献。全部期望值为手工推导的
//     字面量（非引擎回读），见 TestAuditGoldenFixedCase。
//  2. 数据特征覆盖：NaN（因子缺失）、行业缺失（行业上限启用 fail closed）、
//     停牌（不可交易）、涨跌停（买/卖阻断）、T+1（当日买入不可卖）、整手、
//     费用（最低佣金/印花税/滑点）。
//  3. 完整链路（真实 runner）：模型 → 实验 → 产物五类 → MarkCompleted(manifest)
//     → 验证逐窗 → Complete(verdict)；重启 Store 后 hash 重算一致；同输入两次
//     运行 DeepEqual（无测试后选择路径）。
//  4. 泄漏隔离复证：修改测试窗未来收益（构造第二份数据），证明训练窗权重与
//     此前成交不变。

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/internal/portfolioresearch"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/researchdata"
)

// ---- 金标准固定案例（逐项手算） ----

// auditGoldenExec 金标准执行规格：最低佣金 5 元、佣金万三、印花税千一（卖出）、
// 滑点 0.01 元/股、整手 100 股、T+1 开启。
func auditGoldenExec() portfolioresearch.ExecutionSpec {
	return portfolioresearch.ExecutionSpec{
		Rebalance:     portfolioresearch.RebalanceDaily,
		FillAt:        portfolioresearch.FillNextOpen,
		SellFirst:     true,
		T1Restriction: true,
		CarryUnfilled: false,
		LotSize:       100,
		Cost: portfolioresearch.CostSpec{
			CommissionRate:  0.0003,
			StampDutyRate:   0.001,
			TransferFeeRate: 0,
			Slippage:        0.01,
			MinCommission:   5,
		},
	}
}

// auditGoldenPolicy 金标准目标政策：TopN=3、现金缓冲 0、无单票/行业/换手上限、
// 最小持仓 1。
func auditGoldenPolicy() portfolioresearch.PortfolioPolicy {
	return portfolioresearch.PortfolioPolicy{
		Selection:   portfolioresearch.PortfolioSelectionTopN,
		TopN:        3,
		CashBuffer:  0,
		MinHoldings: 1,
	}
}

// auditGoldenPipeline 金标准变换流水线：exclude 缺失、不去极值、不中性化、
// rank 标准化（方向统一后）。
func auditGoldenPipeline() portfolioresearch.TransformPipeline {
	return portfolioresearch.TransformPipeline{
		Missing:     portfolioresearch.TransformMissingExclude,
		Winsorize:   portfolioresearch.WinsorizeSpec{Mode: portfolioresearch.TransformWinsorizeNone},
		Neutralize:  portfolioresearch.NeutralizeSpec{Mode: portfolioresearch.TransformNeutralizeNone},
		Standardize: portfolioresearch.TransformStandardizeRank,
	}
}

// near 浮点近似断言（绝对容差；金标准手算值保留 6 位有效数字）。
func near(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("%s = %.10f, want 手算 %.10f（差 %.2e）", name, got, want, math.Abs(got-want))
	}
}

// TestAuditGoldenFixedCase 金标准固定案例：单信号日 T0 + 单执行日 T1 全链逐项
// 手算断言。6 只股票：sh600001(A)~sh600006(F)。
//
// 信号日 T0="2026-01-05" 原始因子值：
//
//	F1（higher_is_better，模拟动量）: A=3 B=1 C=0 D=-1 E=NaN F=-3
//	F2（lower_is_better，模拟波动）:  A=0.2 B=0.3 C=0.1 D=0.4 E=0.5 F=0.25
//
// 手算（rank 标准化，score=2r/(n+1)-1）：
//
//	F1 有效 5 只（E 缺失剔除），升序名次 F=1 D=2 C=3 B=4 A=5：
//	  A=2/3 B=1/3 C=0 D=-1/3 F=-2/3（higher 保持）
//	F2 有效 6 只，升序名次 C=1 A=2 F=3 B=4 D=5 E=6，lower 翻转：
//	  C=5/7 A=3/7 F=1/7 B=-1/7 D=-3/7 E=-5/7
//	等权合成（exclude → E 无分数）：
//	  A=(2/3+3/7)/2=23/42≈0.547619  B=(1/3-1/7)/2=2/21≈0.095238
//	  C=(0+5/7)/2=5/14≈0.357143   D=(-1/3-3/7)/2=-8/21≈-0.380952
//	  F=(-2/3+1/7)/2=-11/42≈-0.261905
//	TopN=3 → 选中 A、C、B（分数降序），等权 1/3。
//
// 目标（T0 收盘参考价 A=20 C=5 B=10，Notional=1,000,000）：
//
//	Weights   A=C=B=1/3
//	Shares    A=floor(333333.33/20/100)*100=16600
//	          C=floor(333333.33/5/100)*100=66600
//	          B=floor(333333.33/10/100)*100=33300
//	Effective A=16600*20/1e6=0.332  C=66600*5/1e6=0.333  B=33300*10/1e6=0.333
//	Cash      =1-0.998=0.002
//
// 执行日 T1="2026-01-06"（开盘价 A=20.4 C=5.1 B=10.2，前收盘=信号日收盘，
// 收盘价 A=20.5 C=5.05 B=10.3；买入价=开盘+滑点 0.01）：
//
//	买单按分数降序 A(0.547619) → C(0.357143) → B(0.095238)：
//	  A: 16600×20.41=338806，佣金 max(101.6418,5)=101.6418，总 338907.6418
//	  C: 66600×5.11=340326，佣金 102.0978，总 340428.0978
//	  B: 现金剩余 320664.2604 仅够 313 手（31300 股）→ 部分成交 319668.8719，
//	     剩余 2000 股 insufficient_cash 拒绝
//	费用合计 299.6115；期末现金 995.3885
//	期末权益 = 16600×20.5 + 66600×5.05 + 31300×10.3 + 995.3885 = 1000015.3885
//	PnL = 已实现 0 + 公司行为 0 + 未实现(999020-998705)=315
//	对账：1000000+315-299.6115 = 1000015.3885（恒等式成立）
//	净值：NetNav=1000015.3885，GrossNav=1000315（费用累计）
//
// 归因（与 buildAttribution 同口径：期末实际权重×当日收益）：
//
//	股票贡献 = Σ(期末权重×收益)：A 0.34029476×0.025 + C 0.33632483×0.01 +
//	  B 0.32238504×0.03 = 0.02154217；现金贡献 0（rf=0）
//	成本拖累 = 299.6115/1e6 = 0.00029961
//	NetReturn = 0.02154217 - 0.00029961 = 0.02124256
//	选择收益 = (0.025+0.01+0.03)/3 = 0.02166667；执行偏离 = -0.00012450
//	净值口径：PortfolioNetReturn=0.00001539，Residual=-0.02122717（披露复利交叉
//	项与成交时点/整手偏离，不静默抹平）
func TestAuditGoldenFixedCase(t *testing.T) {
	const (
		signalDate = "2026-01-05"
		execDate   = "2026-01-06"
		notional   = 1_000_000.0
		codeA      = "sh600001" // A
		codeB      = "sh600002" // B
		codeC      = "sh600003" // C
		codeD      = "sh600004" // D
		codeE      = "sh600005" // E（F1 缺失）
		codeF      = "sh600006" // F
	)
	pipeline := auditGoldenPipeline()
	exec := auditGoldenExec()
	policy := auditGoldenPolicy()

	// ---- 1) 变换（rank + 方向） ----
	rowsF1 := []portfolioresearch.CrossSectionRow{
		{Date: signalDate, Code: codeA, Value: 3, Valid: true, Tradable: true},
		{Date: signalDate, Code: codeB, Value: 1, Valid: true, Tradable: true},
		{Date: signalDate, Code: codeC, Value: 0, Valid: true, Tradable: true},
		{Date: signalDate, Code: codeD, Value: -1, Valid: true, Tradable: true},
		{Date: signalDate, Code: codeE, Value: math.NaN(), Valid: true, Tradable: true}, // NaN 输入
		{Date: signalDate, Code: codeF, Value: -3, Valid: true, Tradable: true},
	}
	res1, err := portfolioresearch.Transform(pipeline, portfolioresearch.TransformConfig{}, portfolioresearch.DirectionHigherIsBetter, rowsF1, nil)
	if err != nil {
		t.Fatalf("F1 变换失败: %v", err)
	}
	f1Final := map[string]float64{}
	for _, r := range res1.Rows {
		f1Final[r.Code] = r.Final
	}
	near(t, "F1/A final", f1Final[codeA], 2.0/3)
	near(t, "F1/B final", f1Final[codeB], 1.0/3)
	near(t, "F1/C final", f1Final[codeC], 0)
	near(t, "F1/D final", f1Final[codeD], -1.0/3)
	near(t, "F1/F final", f1Final[codeF], -2.0/3)
	if !math.IsNaN(f1Final[codeE]) || res1.Rows[4].Missing != true {
		t.Fatalf("F1/E 应为缺失（NaN 输入不得进入截面）: final=%v missing=%v", f1Final[codeE], res1.Rows[4].Missing)
	}

	rowsF2 := []portfolioresearch.CrossSectionRow{
		{Date: signalDate, Code: codeA, Value: 0.2, Valid: true, Tradable: true},
		{Date: signalDate, Code: codeB, Value: 0.3, Valid: true, Tradable: true},
		{Date: signalDate, Code: codeC, Value: 0.1, Valid: true, Tradable: true},
		{Date: signalDate, Code: codeD, Value: 0.4, Valid: true, Tradable: true},
		{Date: signalDate, Code: codeE, Value: 0.5, Valid: true, Tradable: true},
		{Date: signalDate, Code: codeF, Value: 0.25, Valid: true, Tradable: true},
	}
	res2, err := portfolioresearch.Transform(pipeline, portfolioresearch.TransformConfig{}, portfolioresearch.DirectionLowerIsBetter, rowsF2, nil)
	if err != nil {
		t.Fatalf("F2 变换失败: %v", err)
	}
	f2Final := map[string]float64{}
	for _, r := range res2.Rows {
		f2Final[r.Code] = r.Final
	}
	near(t, "F2/C final", f2Final[codeC], 5.0/7)
	near(t, "F2/A final", f2Final[codeA], 3.0/7)
	near(t, "F2/F final", f2Final[codeF], 1.0/7)
	near(t, "F2/B final", f2Final[codeB], -1.0/7)
	near(t, "F2/D final", f2Final[codeD], -3.0/7)
	near(t, "F2/E final", f2Final[codeE], -5.0/7)

	// ---- z-score 手算对照（标准ize=zscore，F1 同数据：mean=0, std=2） ----
	zPipe := auditGoldenPipeline()
	zPipe.Standardize = portfolioresearch.TransformStandardizeZScore
	zRes, err := portfolioresearch.Transform(zPipe, portfolioresearch.TransformConfig{}, portfolioresearch.DirectionHigherIsBetter, rowsF1, nil)
	if err != nil {
		t.Fatalf("F1 z-score 变换失败: %v", err)
	}
	zFinal := map[string]float64{}
	for _, r := range zRes.Rows {
		zFinal[r.Code] = r.Final
	}
	near(t, "z-score/A", zFinal[codeA], 1.5)
	near(t, "z-score/B", zFinal[codeB], 0.5)
	near(t, "z-score/C", zFinal[codeC], 0)
	near(t, "z-score/D", zFinal[codeD], -0.5)
	near(t, "z-score/F", zFinal[codeF], -1.5)

	// ---- 2) 合成（等权，exclude → E 无分数） ----
	factors := []portfolioresearch.FactorSeries{
		{Key: "mom/2", Direction: portfolioresearch.DirectionHigherIsBetter, Horizon: 1,
			Days: map[string][]portfolioresearch.TransformedRow{signalDate: res1.Rows}},
		{Key: "vol/2", Direction: portfolioresearch.DirectionLowerIsBetter, Horizon: 1,
			Days: map[string][]portfolioresearch.TransformedRow{signalDate: res2.Rows}},
	}
	rep, err := portfolioresearch.Combine(factors, portfolioresearch.TransformMissingExclude, nil)
	if err != nil {
		t.Fatalf("Combine: %v", err)
	}
	if len(rep.Results) != 1 || rep.Results[0].Date != signalDate {
		t.Fatalf("Combine 结果异常: %+v", rep.Results)
	}
	sc := rep.Results[0].Scores
	near(t, "score/A", sc[codeA], 23.0/42)
	near(t, "score/C", sc[codeC], 5.0/14)
	near(t, "score/B", sc[codeB], 2.0/21)
	near(t, "score/D", sc[codeD], -8.0/21)
	near(t, "score/F", sc[codeF], -11.0/42)
	if _, ok := sc[codeE]; ok {
		t.Fatalf("exclude 策略下缺失因子股票 E 不得有分数: %v", sc)
	}

	// ---- 3) 目标权重（TopN=3 → A/C/B，等权 1/3，整手取整） ----
	tgt, err := portfolioresearch.BuildTarget(policy, exec, portfolioresearch.TargetInput{
		Date:       signalDate,
		Scores:     sc,
		Tradable:   map[string]bool{codeA: true, codeB: true, codeC: true, codeD: true, codeF: true},
		Industries: nil,
		Prices:     map[string]float64{codeA: 20, codeC: 5, codeB: 10},
		Holdings:   map[string]float64{},
		CashWeight: 1,
		Notional:   notional,
	})
	if err != nil {
		t.Fatalf("BuildTarget: %v", err)
	}
	w := tgt.Constrained.Weights
	near(t, "target/A", w[codeA], 1.0/3)
	near(t, "target/C", w[codeC], 1.0/3)
	near(t, "target/B", w[codeB], 1.0/3)
	if math.Abs(sumWeights(w)-1) > 1e-6 {
		t.Fatalf("目标权重和 = %.8f, want 1", sumWeights(w))
	}
	if got := tgt.Constrained.Shares[codeA]; got != 16600 {
		t.Fatalf("目标股数 A = %d, want 16600（整手向下取整）", got)
	}
	if got := tgt.Constrained.Shares[codeC]; got != 66600 {
		t.Fatalf("目标股数 C = %d, want 66600", got)
	}
	if got := tgt.Constrained.Shares[codeB]; got != 33300 {
		t.Fatalf("目标股数 B = %d, want 33300", got)
	}
	near(t, "effective/A", tgt.Constrained.Effective[codeA], 0.332)
	near(t, "effective/C", tgt.Constrained.Effective[codeC], 0.333)
	near(t, "effective/B", tgt.Constrained.Effective[codeB], 0.333)
	near(t, "target cash", tgt.Constrained.Cash, 0.002)

	// ---- 4) 执行（成交价格/数量/费用、现金、净值、拒绝） ----
	market := portfolioresearch.DayMarket{
		Date:      execDate,
		OpenPrice: map[string]float64{codeA: 20.4, codeC: 5.1, codeB: 10.2},
		PrevClose: map[string]float64{codeA: 20, codeC: 5, codeB: 10},
		ClosePrice: map[string]float64{
			codeA: 20.5, codeC: 5.05, codeB: 10.3, codeD: 8, codeE: 9, codeF: 7,
		},
		Tradable: map[string]bool{codeA: true, codeC: true, codeB: true},
		LimitUp:  map[string]bool{}, LimitDown: map[string]bool{},
		Listed: map[string]bool{codeA: true, codeC: true, codeB: true},
	}
	ledger, _, err := portfolioresearch.ExecuteDay(
		portfolioresearch.PortfolioState{Cash: notional},
		portfolioresearch.DayInput{Target: tgt, Market: market, Scores: sc},
		exec,
	)
	if err != nil {
		t.Fatalf("ExecuteDay: %v", err)
	}
	// 三笔买单按分数降序 A→C→B；B 部分成交（现金不足）。
	if len(ledger.Fills) != 3 {
		t.Fatalf("成交数 = %d, want 3: %+v", len(ledger.Fills), ledger.Fills)
	}
	fillA, fillC, fillB := ledger.Fills[0], ledger.Fills[1], ledger.Fills[2]
	if fillA.Code != codeA || fillA.Side != portfolioresearch.SideBuy || fillA.Shares != 16600 {
		t.Fatalf("fillA 异常: %+v", fillA)
	}
	near(t, "fillA 价格", fillA.Price, 20.41) // 开盘 20.4 + 滑点 0.01
	near(t, "fillA 毛额", fillA.GrossAmount, 338806)
	near(t, "fillA 佣金", fillA.Fees.Commission, 101.6418)
	near(t, "fillA 净额", fillA.NetAmount, 338907.6418)
	if fillC.Code != codeC || fillC.Shares != 66600 {
		t.Fatalf("fillC 异常: %+v", fillC)
	}
	near(t, "fillC 价格", fillC.Price, 5.11)
	near(t, "fillC 毛额", fillC.GrossAmount, 340326)
	near(t, "fillC 佣金", fillC.Fees.Commission, 102.0978)
	near(t, "fillC 净额", fillC.NetAmount, 340428.0978)
	if fillB.Code != codeB || fillB.Shares != 31300 || !fillB.Partial || fillB.Requested != 33300 {
		t.Fatalf("fillB 异常（应部分成交 31300/33300）: %+v", fillB)
	}
	near(t, "fillB 价格", fillB.Price, 10.21)
	near(t, "fillB 毛额", fillB.GrossAmount, 319573)
	near(t, "fillB 佣金", fillB.Fees.Commission, 95.8719)
	near(t, "fillB 净额", fillB.NetAmount, 319668.8719)
	if len(ledger.Rejections) != 1 || ledger.Rejections[0].Reason != portfolioresearch.UnfilledInsufficientCash ||
		ledger.Rejections[0].Shares != 2000 {
		t.Fatalf("拒绝记录异常（B 剩余 2000 股现金不足）: %+v", ledger.Rejections)
	}
	near(t, "费用合计", ledger.Fees.Total(), 299.6115)
	near(t, "期末现金", ledger.EndCash, 995.3885)
	near(t, "期末权益", ledger.EndEquity, 1000015.3885)
	near(t, "当日损益", ledger.Pnl, 315)
	if !ledger.Reconciled || math.Abs(ledger.ReconResidual) > 1e-6 {
		t.Fatalf("对账应成立: reconciled=%v residual=%v", ledger.Reconciled, ledger.ReconResidual)
	}
	// T+1：当日买入不可卖（BuyDate == 执行日）→ 卖出意图拒绝 t1_restricted。
	t1Ledger, _, err := portfolioresearch.ExecuteDay(
		portfolioresearch.PortfolioState{Cash: 100000, Lots: []portfolioresearch.HoldingLot{
			{Code: codeA, Shares: 100, BuyPrice: 20.41, CostBasis: 2041, BuyDate: execDate},
		}},
		portfolioresearch.DayInput{
			Target: portfolioresearch.TargetPortfolio{Constrained: portfolioresearch.PortfolioWeights{
				Shares: map[string]int{codeA: 0},
			}},
			Market: portfolioresearch.DayMarket{
				Date: execDate, OpenPrice: map[string]float64{codeA: 20.4},
				PrevClose: map[string]float64{codeA: 20}, ClosePrice: map[string]float64{codeA: 20.5},
				Tradable: map[string]bool{codeA: true}, Listed: map[string]bool{codeA: true},
			},
			Scores: nil,
		}, exec)
	if err != nil {
		t.Fatalf("T+1 卖出执行失败: %v", err)
	}
	if len(t1Ledger.Rejections) != 1 || t1Ledger.Rejections[0].Reason != portfolioresearch.UnfilledT1Restricted {
		t.Fatalf("当日买入当日卖出应拒绝 t1_restricted: %+v", t1Ledger.Rejections)
	}
	if len(t1Ledger.EndHoldings) != 1 || t1Ledger.EndHoldings[0].Shares != 100 {
		t.Fatalf("T+1 拒绝后持仓应保留: %+v", t1Ledger.EndHoldings)
	}

	// ---- 5) 净值（毛/净） ----
	series := portfolioresearch.FromLedgers([]portfolioresearch.DailyLedger{ledger})
	if len(series.Days) != 1 {
		t.Fatalf("净值点数 = %d, want 1", len(series.Days))
	}
	near(t, "NetNav", series.Days[0].NetNav, 1000015.3885)
	near(t, "GrossNav", series.Days[0].GrossNav, 1000315) // 净净值 + 累计费用

	// ---- 6) 归因贡献（与 buildAttribution 同口径） ----
	actual := map[string]float64{}
	for _, lot := range ledger.EndHoldings {
		actual[lot.Code] = float64(lot.Shares) * market.ClosePrice[lot.Code] / ledger.EndEquity
	}
	att, err := portfolioresearch.ComputeAttribution(portfolioresearch.AttributionInput{
		InitialEquity: notional,
		RiskFreeRate:  0,
		Days: []portfolioresearch.AttributionDay{{
			Date:          execDate,
			ActualWeights: actual,
			CashRatio:     ledger.EndCash / ledger.EndEquity,
			Returns:       map[string]float64{codeA: 0.025, codeC: 0.01, codeB: 0.03},
			Fees:          ledger.Fees.Total(),
			TargetWeights: tgt.Constrained.Effective,
			IdealWeights:  tgt.Ideal.Weights,
			GrossNav:      series.Days[0].GrossNav,
			NetNav:        series.Days[0].NetNav,
		}},
	})
	if err != nil {
		t.Fatalf("ComputeAttribution: %v", err)
	}
	near(t, "归因股票贡献合计", att.StockCashSum, 0.0215421688)
	near(t, "归因成本拖累", att.CostDrag, 0.0002996115)
	near(t, "归因净收益", att.NetReturn, 0.0212425565)
	if att.SelectionReturn == nil || att.ExecutionDeviation == nil {
		t.Fatal("理想目标已提供，选择收益/执行偏离不应为 nil")
	}
	near(t, "归因选择收益", *att.SelectionReturn, 0.0216666667)
	near(t, "归因执行偏离", *att.ExecutionDeviation, -0.0001244999)
	if att.PortfolioNetReturn == nil || att.PortfolioGrossReturn == nil || att.Residual == nil {
		t.Fatal("净值口径归因对照缺失")
	}
	near(t, "净值口径毛收益", *att.PortfolioGrossReturn, 0.000315)
	near(t, "净值口径净收益", *att.PortfolioNetReturn, 0.0000153885)
	near(t, "归因残差（披露）", *att.Residual, -0.0212271680)
	if !att.Reconciled {
		t.Fatal("归因恒等式应成立（StockCashSum/NetReturn 构造性）")
	}
}

// sumWeights 权重和（金标准辅助）。
func sumWeights(m map[string]float64) float64 {
	s := 0.0
	for _, v := range m {
		s += v
	}
	return s
}

// ---- 数据特征覆盖（停牌/涨跌停/T+1/行业缺失） ----

// TestAuditExecutionEdgeScenarios 停牌（卖不出）、涨跌停（买/卖阻断）、
// T+1（当日买入不可卖）、行业缺失（fail closed）。全部为固定小案例手算可验证。
func TestAuditExecutionEdgeScenarios(t *testing.T) {
	exec := auditGoldenExec()

	// 场景 1：持仓停牌 → 卖出拒绝（UnfilledSuspended），持仓保留。
	held := []portfolioresearch.HoldingLot{{Code: "sh600001", Shares: 1000, BuyPrice: 10, CostBasis: 10000, BuyDate: "2026-01-05"}}
	market := portfolioresearch.DayMarket{
		Date:       "2026-01-06",
		OpenPrice:  map[string]float64{"sh600001": 10.5},
		PrevClose:  map[string]float64{"sh600001": 10},
		ClosePrice: map[string]float64{"sh600001": 10.5},
		Tradable:   map[string]bool{"sh600001": false}, // 停牌
		Listed:     map[string]bool{"sh600001": true},
	}
	tgt := portfolioresearch.TargetPortfolio{Constrained: portfolioresearch.PortfolioWeights{Shares: map[string]int{"sh600001": 0}}}
	ledger, _, err := portfolioresearch.ExecuteDay(
		portfolioresearch.PortfolioState{Cash: 100000, Lots: held},
		portfolioresearch.DayInput{Target: tgt, Market: market, Scores: nil}, exec)
	if err != nil {
		t.Fatalf("停牌卖出执行失败: %v", err)
	}
	if len(ledger.Rejections) != 1 || ledger.Rejections[0].Reason != portfolioresearch.UnfilledSuspended {
		t.Fatalf("停牌卖出应拒绝 suspended: %+v", ledger.Rejections)
	}
	if len(ledger.EndHoldings) != 1 || ledger.EndHoldings[0].Shares != 1000 {
		t.Fatalf("停牌持仓应保留: %+v", ledger.EndHoldings)
	}

	// 场景 2：涨停买不到 / 跌停卖不掉。
	market2 := portfolioresearch.DayMarket{
		Date:       "2026-01-06",
		OpenPrice:  map[string]float64{"sh600001": 10.5, "sh600002": 20},
		PrevClose:  map[string]float64{"sh600001": 10, "sh600002": 20},
		ClosePrice: map[string]float64{"sh600001": 10.5, "sh600002": 20},
		Tradable:   map[string]bool{"sh600001": true, "sh600002": true},
		LimitUp:    map[string]bool{"sh600001": true}, // 涨停：买不到
		LimitDown:  map[string]bool{"sh600002": true}, // 跌停：卖不掉
		Listed:     map[string]bool{"sh600001": true, "sh600002": true},
	}
	tgt2 := portfolioresearch.TargetPortfolio{Constrained: portfolioresearch.PortfolioWeights{
		Shares:    map[string]int{"sh600001": 100},
		Effective: map[string]float64{"sh600001": 0.1},
	}}
	ledger2, _, err := portfolioresearch.ExecuteDay(
		portfolioresearch.PortfolioState{Cash: 100000, Lots: []portfolioresearch.HoldingLot{
			{Code: "sh600002", Shares: 1000, BuyPrice: 18, CostBasis: 18000, BuyDate: "2026-01-05"},
		}},
		portfolioresearch.DayInput{Target: tgt2, Market: market2, Scores: map[string]float64{"sh600001": 1}}, exec)
	if err != nil {
		t.Fatalf("涨跌停执行失败: %v", err)
	}
	reasons := map[string]bool{}
	for _, r := range ledger2.Rejections {
		reasons[r.Reason] = true
	}
	if !reasons[portfolioresearch.UnfilledLimitUp] {
		t.Fatalf("涨停买入应拒绝 limit_up: %+v", ledger2.Rejections)
	}
	if !reasons[portfolioresearch.UnfilledLimitDown] {
		t.Fatalf("跌停卖出应拒绝 limit_down: %+v", ledger2.Rejections)
	}

	// 场景 3：行业缺失 fail closed——行业上限启用时选中股票缺行业 → 错误。
	_, err = portfolioresearch.BuildTarget(
		portfolioresearch.PortfolioPolicy{Selection: portfolioresearch.PortfolioSelectionTopN, TopN: 1,
			MaxIndustryWeight: 0.5, CashBuffer: 0, MinHoldings: 1},
		exec,
		portfolioresearch.TargetInput{
			Date:       "2026-01-05",
			Scores:     map[string]float64{"sh600001": 0.5, "sh600002": 1.0}, // Top1 选中 sh600002（缺行业）
			Tradable:   map[string]bool{"sh600001": true, "sh600002": true},
			Industries: map[string]string{"sh600001": "ind1"}, // sh600002 缺行业
			Prices:     map[string]float64{"sh600001": 10, "sh600002": 20},
			Holdings:   map[string]float64{}, CashWeight: 1,
			Notional: 1_000_000,
		})
	if err == nil || !strings.Contains(err.Error(), "行业") {
		t.Fatalf("行业上限启用且选中股票缺行业应 fail closed: %v", err)
	}

	// 场景 4（审计观察，记录语义）：无任何单票/行业上限配置时，选中股票在
	// 可交易性预检查被剔除后，释放权重不会重新分配（capSolve 仅在至少一个
	// 上限启用时水填充），随后 StepTarget 权重和校验失败 → 结构化 insufficient
	// （fail closed，不静默放宽；但设计 §8.2 注释声称"剔除后权重水填充给剩余
	// 股票"，实现与该注释在无上限场景不一致，已记录为 Minor 观察项）。
	_, err = portfolioresearch.BuildTarget(
		portfolioresearch.PortfolioPolicy{Selection: portfolioresearch.PortfolioSelectionTopN, TopN: 2,
			CashBuffer: 0, MinHoldings: 1},
		exec,
		portfolioresearch.TargetInput{
			Date: "2026-01-05",
			Scores: map[string]float64{
				"sh600001": 2, "sh600002": 1,
			},
			Tradable:   map[string]bool{"sh600001": true, "sh600002": false}, // 选中且不可交易
			Industries: nil,
			Prices:     map[string]float64{"sh600001": 10, "sh600002": 20},
			Holdings:   map[string]float64{}, CashWeight: 1,
			Notional: 1_000_000,
		})
	if err == nil {
		t.Fatalf("无上限 + 不可交易选中股票：当前实现应 fail closed（StepTarget 权重和不足）而非静默放宽")
	}
	var insuff *portfolioresearch.InsufficientError
	if !errors.As(err, &insuff) || insuff.Step != portfolioresearch.StepTarget {
		t.Fatalf("无上限 + 不可交易选中股票应返回 StepTarget insufficient，实际 %T: %v", err, err)
	}
}

// ---- 完整链路（真实 runner：模型 → 实验 → 产物 → 验证） ----

// writeAuditE2EData 6 只股票：sh600001~600003 平滑上行、600004~600006 平滑下行，
// 35 个交易日自 2024-12-24 起（动量/波动因子名次稳定：上行票动量高、波动 0，
// 下行票动量负；等权秩合成下 Top3 恒为上行票）。
func writeAuditE2EData(t *testing.T, dir string) {
	t.Helper()
	base := time.Date(2024, 12, 24, 0, 0, 0, 0, time.Local)
	for j := 0; j < 3; j++ {
		up := make([]float64, 35)
		for i := range up {
			up[i] = 10 + 0.2*float64(i) // 10.0 → 16.8
		}
		writeDayDBCloses(t, dir, "sh60000"+string(rune('1'+j)), up, base)
	}
	for j := 0; j < 3; j++ {
		down := make([]float64, 35)
		for i := range down {
			down[i] = 20 - 0.2*float64(i) // 20.0 → 13.2
		}
		writeDayDBCloses(t, dir, "sh60000"+string(rune('4'+j)), down, base)
	}
}

// auditE2EFixture 6 股完整可运行链路（模型/实验/验证三库 + 注入静态股票池）。
type auditE2EFixture struct {
	runner       *Runner
	models       *FactorModelStore
	experiments  *PortfolioExperimentStore
	validations  *PortfolioValidationStore
	model        portfolioresearch.FactorModel
	modelRoot    string
	expRecords   string
	expArtifacts string
	pvRoot       string
}

func newAuditE2EFixture(t *testing.T) *auditE2EFixture {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	old := common.Pull
	common.Pull = &extend.PullKline{Config: extend.PullKlineConfig{Dir: dir}}
	t.Cleanup(func() { common.Pull = old })
	writeAuditE2EData(t, dir)

	store := NewFactorModelStore(t.TempDir())
	// 直接落盘合法模型（writePortfolioModel 固定 TopN=2、现金缓冲 5%）：6 只
	// 股票中 3 只上行、3 只下行，等权秩合成下 Top2 恒为上行票（动量正 + 波动
	// 0），固定案例净收益为正。
	m := writePortfolioModel(t, store, "fm_20260919T000000000Z_90000011",
		[]portfolioresearch.ValidatedFactorRef{
			factorRef("fc_20260919T000000000Z_00000011", "fv_20260919T000000000Z_00000011", "momentum", 2),
			factorRef("fc_20260919T000000000Z_00000012", "fv_20260919T000000000Z_00000012", "volatility", 2),
		},
		portfolioresearch.CombinationSpec{Method: portfolioresearch.CombinationEqualWeightRank})
	expRecords, expArtifacts := t.TempDir(), t.TempDir()
	pvRoot := t.TempDir()
	experiments := NewPortfolioExperimentStore(expRecords, expArtifacts)
	validations := NewPortfolioValidationStore(pvRoot)
	r := NewRunner()
	r.universe = researchdata.NewStaticUniverse(researchdata.StaticUniverseConfig{
		ID: "audit-static", Source: "test", Codes: func() []string {
			return []string{"sh600001", "sh600002", "sh600003", "sh600004", "sh600005", "sh600006"}
		},
	})
	r.ConfigurePortfolio(store, experiments, validations)
	return &auditE2EFixture{
		runner: r, models: store, experiments: experiments, validations: validations,
		model: m, modelRoot: store.root, expRecords: expRecords,
		expArtifacts: expArtifacts, pvRoot: pvRoot,
	}
}

// auditExperimentReq 指向给定模型的实验请求（研究区间 2025-01-01 ~ 2025-02-15，
// 覆盖 35 个数据日中的 32 个在区间内）。
func auditExperimentReq(m portfolioresearch.FactorModel, requestID string) CreatePortfolioExperimentRequest {
	return CreatePortfolioExperimentRequest{
		RequestID:     requestID,
		FamilyID:      "portfolio-audit-e2e",
		ModelID:       m.ModelID,
		ModelRevision: m.Revision,
		ModelHash:     m.ModelHash,
		StudyRange:    DateRange{Start: "2025-01-01", End: "2025-02-15"},
		Variant:       ParameterVariant{Name: "audit-e2e", Desc: "Task 11 端到端审计"},
		VariantSource: ExperimentVariantDeclared,
		DataSnapshot: portfolioresearch.DataSnapshot{
			UniverseMode: "historical_membership", PriceSource: "local-klines",
			PriceVersion: "2026-01", PITState: "verified",
		},
		CodeVersion:   "v2-task11-audit",
		EvidenceClass: m.EvidenceClass,
		TrialCounted:  true,
		CountReason:   "Task 11 端到端审计，计入试验次数",
	}
}

// TestAuditE2EFullChain 完整链路：模型冻结（modelId/revision/hash）→ 实验
// Create/Start → runner 运行 → 五类产物 → MarkCompleted(manifest) → 验证 Create
// → 逐窗 → Complete(verdict)。断言完成门槛：manifest 五类齐全、报告 hash 一致、
// 产物磁盘存在、验证结论可逐窗解释。
func TestAuditE2EFullChain(t *testing.T) {
	f := newAuditE2EFixture(t)
	// 模型身份：modelId/revision/hash 已冻结（Create 时后端派生）。
	if f.model.ModelID == "" || f.model.Revision != 1 || len(f.model.ModelHash) != 64 {
		t.Fatalf("模型身份异常: %+v", f.model)
	}

	// 实验创建 + 运行。
	exp, created, err := f.experiments.Create(auditExperimentReq(f.model, testUUID1))
	if err != nil || !created {
		t.Fatalf("创建实验 = %v/%v", created, err)
	}
	if exp.Status != portfolioresearch.RunStateQueued {
		t.Fatalf("实验初始状态 = %q, want queued", exp.Status)
	}
	if err := f.runner.StartPortfolioExperiment(exp.ExperimentID); err != nil {
		t.Fatalf("启动实验: %v", err)
	}
	st := waitPortfolioDone(t, f.runner)
	if st["state"] != "done" {
		t.Fatalf("组合任务应为 done: %v", st)
	}
	got, err := f.experiments.Get(exp.ExperimentID)
	if err != nil {
		t.Fatalf("读取实验: %v", err)
	}
	if got.Status != portfolioresearch.RunStateCompleted {
		t.Fatalf("终态 = %q, want completed", got.Status)
	}
	if got.Manifest == nil || len(got.Manifest.Entries) != 5 {
		t.Fatalf("completed 必须携带五类产物 manifest: %+v", got.Manifest)
	}
	if err := got.Manifest.Validate(); err != nil {
		t.Fatalf("manifest 校验失败: %v", err)
	}
	if got.ReportHash != got.Manifest.hashOf("report.json") || got.ReportPath != "report.json" {
		t.Fatalf("报告 hash/路径异常: %q/%q", got.ReportHash, got.ReportPath)
	}
	// 五类产物磁盘存在且非空。
	for _, name := range portfolioArtifactNames {
		p := filepath.Join(f.expArtifacts, exp.ExperimentID, name)
		fi, err := os.Stat(p)
		if err != nil || fi.Size() == 0 {
			t.Fatalf("产物 %s 缺失或为空: %v", name, err)
		}
	}
	// report.json 可反序列化且核心语义自洽。
	data, err := os.ReadFile(filepath.Join(f.expArtifacts, exp.ExperimentID, "report.json"))
	if err != nil {
		t.Fatalf("读 report.json: %v", err)
	}
	var rep PortfolioReport
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("report.json 反序列化失败: %v", err)
	}
	if rep.ExperimentID != exp.ExperimentID || rep.ModelID != f.model.ModelID ||
		rep.ModelHash != f.model.ModelHash || rep.EvidenceClass != f.model.EvidenceClass {
		t.Fatalf("报告语义字段异常: %+v", rep)
	}
	if rep.Metrics.TradingDays == 0 || rep.Attribution.NetReturn == 0 || len(rep.Nav) == 0 {
		t.Fatalf("报告应含指标/归因/净值: metrics=%+v attribution=%+v nav=%d",
			rep.Metrics, rep.Attribution, len(rep.Nav))
	}

	// 验证：Create → 逐窗 → Complete（verdict 由后端生成）。
	rec, createdV, err := f.validations.Create(CreatePortfolioValidationRequest{
		RequestID: testUUID2,
		Spec: portfolioresearch.PortfolioValidationSpec{
			ModelRef:   portfolioresearch.ModelRef{ModelID: f.model.ModelID, Revision: f.model.Revision, Hash: f.model.ModelHash},
			WindowRule: portfolioresearch.WindowRule{TrainDays: 5, TestDays: 5, Step: 5},
			Gates: portfolioresearch.GateSpec{
				MinValidWindows: 1, MinTradingDays: 10, MinNetReturn: 0.01,
			},
			Benchmark: portfolioresearch.BenchmarkSpec{ID: "hs300"},
		},
	}, f.models)
	if err != nil || !createdV {
		t.Fatalf("创建验证 = %v/%v", createdV, err)
	}
	if rec.SpecHash == "" || rec.ModelEvidenceClass != portfolioresearch.EvidenceRetrospective {
		t.Fatalf("验证冻结记录异常: %+v", rec)
	}
	if err := f.runner.StartPortfolioValidation(rec.ID); err != nil {
		t.Fatalf("启动验证: %v", err)
	}
	waitPortfolioDone(t, f.runner)
	view, err := f.validations.Get(rec.ID)
	if err != nil {
		t.Fatalf("读取验证: %v", err)
	}
	if view.State != PortfolioValidationStateCompleted || view.Report == nil {
		t.Fatalf("验证应完成并发布报告: state=%q", view.State)
	}
	// verdict 可逐窗解释：每窗有逐项 GateResult；聚合结论与冻结 spec 唯一对应。
	if !portfolioresearch.ValidGateStatus(view.Report.Verdict) {
		t.Fatalf("verdict 非法: %q", view.Report.Verdict)
	}
	if len(view.Windows) == 0 {
		t.Fatal("验证应有窗口结果")
	}
	for i, w := range view.Windows {
		if w.Index != i+1 {
			t.Fatalf("窗口序号不连续: %d", w.Index)
		}
		if w.State != portfolioresearch.WindowStateOK || w.Metrics == nil {
			t.Fatalf("窗口 %d 应 ok 且带指标: %+v", i+1, w)
		}
		if len(w.Gates) == 0 {
			t.Fatalf("窗口 %d 应携带逐项门禁（结论可逐窗解释）", i+1)
		}
		// 训练/测试区间次序：训练严格早于测试（泄漏隔离）。
		if w.TrainEnd >= w.TestStart {
			t.Fatalf("窗口 %d 训练结束 %s 不早于测试开始 %s", i+1, w.TrainEnd, w.TestStart)
		}
	}
	// 上行票占 Top3 → 净收益为正 → 门禁通过。
	if view.Report.Verdict != portfolioresearch.GateStatusPassed {
		t.Fatalf("固定案例应 passed，实际 %q（message=%s）", view.Report.Verdict, view.Report.Message)
	}

	// 重启后验证可读且 verdict/证据等级重算一致（fail closed）。
	pvs2 := NewPortfolioValidationStore(f.pvRoot)
	view2, err := pvs2.Get(rec.ID)
	if err != nil {
		t.Fatalf("重启后读取验证: %v", err)
	}
	if view2.State != PortfolioValidationStateCompleted || view2.Report == nil {
		t.Fatalf("重启后验证应 completed: state=%q", view2.State)
	}
	if view2.Report.Verdict != view.Report.Verdict || view2.Report.EvidenceClass != view.Report.EvidenceClass {
		t.Fatalf("重启后 verdict/证据等级重算不一致: %q/%q vs %q/%q",
			view2.Report.Verdict, view2.Report.EvidenceClass, view.Report.Verdict, view.Report.EvidenceClass)
	}
	if !reflect.DeepEqual(view2.Report.Gates, view.Report.Gates) {
		t.Fatal("重启后逐项门禁重算不一致")
	}
}

// TestAuditE2EDeterminismAndRestart 确定性（同输入两次运行 DeepEqual）+ 重启
// Store（重建实例指向同一根目录）后 hash 重算一致。
func TestAuditE2EDeterminismAndRestart(t *testing.T) {
	f := newAuditE2EFixture(t)
	run := func(requestID string) PortfolioExperiment {
		t.Helper()
		exp, created, err := f.experiments.Create(auditExperimentReq(f.model, requestID))
		if err != nil || !created {
			t.Fatalf("创建实验(%s) = %v/%v", requestID, created, err)
		}
		if err := f.runner.StartPortfolioExperiment(exp.ExperimentID); err != nil {
			t.Fatalf("启动实验: %v", err)
		}
		waitPortfolioDone(t, f.runner)
		got, err := f.experiments.Get(exp.ExperimentID)
		if err != nil {
			t.Fatalf("读取实验: %v", err)
		}
		if got.Status != portfolioresearch.RunStateCompleted {
			t.Fatalf("终态 = %q, want completed", got.Status)
		}
		return got
	}
	exp1 := run(testUUID1)
	exp2 := run(testUUID2) // 同输入（仅 requestId 不同）→ 无测试后选择路径

	readReport := func(id string) PortfolioReport {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(f.expArtifacts, id, "report.json"))
		if err != nil {
			t.Fatal(err)
		}
		var rep PortfolioReport
		if err := json.Unmarshal(data, &rep); err != nil {
			t.Fatal(err)
		}
		return rep
	}
	rep1, rep2 := readReport(exp1.ExperimentID), readReport(exp2.ExperimentID)
	// 时间戳（StartedAt/FinishedAt）为运行时刻信息，不参与确定性比对；其余
	// 指标/归因/净值必须逐位一致。
	if !reflect.DeepEqual(rep1.Metrics, rep2.Metrics) {
		t.Fatal("两次运行 Metrics 不一致（存在非确定性）")
	}
	if !reflect.DeepEqual(rep1.Attribution, rep2.Attribution) {
		jb1, _ := json.Marshal(rep1.Attribution)
		jb2, _ := json.Marshal(rep2.Attribution)
		t.Fatalf("两次运行 Attribution 不一致（存在非确定性）:\nrun1=%s\nrun2=%s", jb1, jb2)
	}
	if !reflect.DeepEqual(rep1.Nav, rep2.Nav) {
		t.Fatal("两次运行 Nav 不一致（存在非确定性）")
	}
	// CSV 产物逐字节一致（不依赖时间戳，是真正的确定性证据）。
	for _, name := range []string{"nav.csv", "orders.csv", "trades.csv", "holdings.csv"} {
		b1, err1 := os.ReadFile(filepath.Join(f.expArtifacts, exp1.ExperimentID, name))
		b2, err2 := os.ReadFile(filepath.Join(f.expArtifacts, exp2.ExperimentID, name))
		if err1 != nil || err2 != nil {
			t.Fatalf("读 CSV %s: %v/%v", name, err1, err2)
		}
		if !reflect.DeepEqual(b1, b2) {
			t.Fatalf("两次运行 %s 不一致", name)
		}
	}
	// 两次运行 CSV 产物的 SHA-256 也应一致（manifest 的确定性部分）。
	if exp1.Manifest.hashOf("nav.csv") != exp2.Manifest.hashOf("nav.csv") ||
		exp1.Manifest.hashOf("orders.csv") != exp2.Manifest.hashOf("orders.csv") {
		t.Fatal("两次运行 CSV 产物 hash 不一致")
	}
	// 五类产物名清单一致（manifest 结构确定性；report.json hash 因 FinishedAt
	// 时间戳不同而不同，属报告完成时刻的合法信息，不参与确定性比对）。
	for i := range exp1.Manifest.Entries {
		if exp1.Manifest.Entries[i].Name != exp2.Manifest.Entries[i].Name {
			t.Fatalf("两次运行 manifest 产物名不一致: %s vs %s",
				exp1.Manifest.Entries[i].Name, exp2.Manifest.Entries[i].Name)
		}
	}

	// 重启：同一根目录新建全部存储（等价进程重启）。
	models2 := NewFactorModelStore(f.modelRoot)
	exps2 := NewPortfolioExperimentStore(f.expRecords, f.expArtifacts)
	if _, err := models2.Get(f.model.ModelID, f.model.Revision); err != nil {
		t.Fatalf("重启后模型 hash 重算校验失败: %v", err)
	}
	got2, err := exps2.Get(exp1.ExperimentID)
	if err != nil {
		t.Fatalf("重启后读取实验: %v", err)
	}
	if got2.Status != portfolioresearch.RunStateCompleted || got2.Manifest == nil {
		t.Fatalf("重启后终态应从 Store 恢复: %+v", got2)
	}
	// completed 读取重算磁盘产物 hash 与清单比对（Get 内 fail closed）。
	paths, err := exps2.ArtifactPaths(exp1.ExperimentID)
	if err != nil || len(paths) != 5 {
		t.Fatalf("重启后产物索引 = %v/%v", paths, err)
	}
	// 篡改产物 → 重启后 Get 必须 fail closed。
	tampered := filepath.Join(f.expArtifacts, exp1.ExperimentID, "nav.csv")
	if err := os.WriteFile(tampered, []byte("tampered"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := exps2.Get(exp1.ExperimentID); err == nil || !strings.Contains(err.Error(), "哈希不匹配") {
		t.Fatalf("篡改产物后 Get 应 fail closed（哈希不匹配），实际: %v", err)
	}
}

// ---- 泄漏隔离复证 ----

// auditLeakData 构造 portfolioRunData：11 个交易日、6 只股票、2 因子。future 为
// true 时第 9~11 日价格与因子值被修改（测试窗未来收益），此前 8 日逐位相同。
func auditLeakData(future bool) *portfolioRunData {
	dates := make([]string, 11)
	for i := range dates {
		dates[i] = time.Date(2026, 1, 5, 0, 0, 0, 0, time.Local).AddDate(0, 0, i).Format(time.DateOnly)
	}
	codes := []string{"sh600001", "sh600002", "sh600003", "sh600004", "sh600005", "sh600006"}
	closeM := map[string]map[string]float64{}
	openM := map[string]map[string]float64{}
	values := map[string]map[string]map[string]float64{
		"k1": {}, "k2": {},
	}
	for i, d := range dates {
		closeM[d] = map[string]float64{}
		openM[d] = map[string]float64{}
		for j, c := range codes {
			base := 10 + float64(j)*0.5 + float64(i)*0.3
			if future && i >= 8 {
				base *= 0.8 // 测试窗未来收益变化
			}
			closeM[d][c] = base
			openM[d][c] = base - 0.05
			values["k1"][d] = map[string]float64{}
			values["k2"][d] = map[string]float64{}
			v1 := 0.1*float64(i+1) + 0.05*float64(j)
			v2 := 0.3 - 0.04*float64(j) + 0.02*float64(i)
			if future && i >= 8 {
				v1 = -v1
				v2 = -v2
			}
			values["k1"][d][c] = v1
			values["k2"][d][c] = v2
		}
	}
	prevClose := map[string]map[string]float64{}
	for i, d := range dates {
		if i == 0 {
			continue
		}
		prevClose[d] = closeM[dates[i-1]]
	}
	return &portfolioRunData{
		codes: codes, dates: dates, values: values,
		close: closeM, open: openM, prevClose: prevClose,
	}
}

// auditLeakFactors 从 data.values 构造两因子序列（方向统一后的 Final 直接使用）。
func auditLeakFactors(data *portfolioRunData) []portfolioresearch.FactorSeries {
	build := func(key string, dir string) portfolioresearch.FactorSeries {
		days := map[string][]portfolioresearch.TransformedRow{}
		for _, d := range data.dates {
			var rows []portfolioresearch.TransformedRow
			for _, c := range data.codes {
				rows = append(rows, portfolioresearch.TransformedRow{Code: c, Original: data.values[key][d][c], Final: data.values[key][d][c]})
			}
			days[d] = rows
		}
		return portfolioresearch.FactorSeries{Key: key, Direction: dir, Horizon: 1, Days: days}
	}
	return []portfolioresearch.FactorSeries{
		build("k1", portfolioresearch.DirectionHigherIsBetter),
		build("k2", portfolioresearch.DirectionLowerIsBetter),
	}
}

// auditGoldenModel 金标准模型（policy/exec/transform 与手算场景一致；供
// runPortfolioDays 等 runner 链函数使用）。
func auditGoldenModel() portfolioresearch.FactorModel {
	return portfolioresearch.FactorModel{
		ModelID:          "fm_20260919T000000000Z_a0000001",
		Revision:         1,
		CreatedAt:        "2026-09-19T00:00:00Z",
		CreatedBy:        "audit",
		ResearchQuestion: "Task 11 端到端金标准固定案例",
		Hypothesis:       "手算链路复证",
		ValidatedFactors: []portfolioresearch.ValidatedFactorRef{
			{CandidateID: "fc_a1", CandidateRevision: 1, ValidationID: "fv_a1",
				FactorKind: "momentum", FactorDays: 2, ImplementationVersion: 1,
				Direction:     portfolioresearch.DirectionHigherIsBetter,
				EvidenceClass: portfolioresearch.EvidenceRetrospective, PrimaryHorizon: 1,
				DataSnapshot: portfolioresearch.DataSnapshot{UniverseMode: "current_static", PriceSource: "local-klines"}},
			{CandidateID: "fc_a2", CandidateRevision: 1, ValidationID: "fv_a2",
				FactorKind: "volatility", FactorDays: 2, ImplementationVersion: 1,
				Direction:     portfolioresearch.DirectionLowerIsBetter,
				EvidenceClass: portfolioresearch.EvidenceRetrospective, PrimaryHorizon: 1,
				DataSnapshot: portfolioresearch.DataSnapshot{UniverseMode: "current_static", PriceSource: "local-klines"}},
		},
		TransformPipeline: auditGoldenPipeline(),
		Combination:       portfolioresearch.CombinationSpec{Method: portfolioresearch.CombinationEqualWeightRank},
		PortfolioPolicy:   auditGoldenPolicy(),
		Execution:         auditGoldenExec(),
		Benchmark:         portfolioresearch.BenchmarkSpec{ID: "hs300"},
		EvidenceClass:     portfolioresearch.EvidenceRetrospective,
		CodeVersion:       "v2-task11-audit",
		DataSnapshot: portfolioresearch.DataSnapshot{
			UniverseMode: "current_static", PriceSource: "local-klines",
		},
	}
}

// TestAuditLeakIsolationFutureReturns 泄漏隔离复证：构造两份数据（原版/测试窗
// 未来收益修改版），前 8 日逐位相同。滚动 IC 权重只由训练窗（前 8 日）数据
// 决定 → 两份数据权重逐位一致；前 8 日成交/现金/净值逐位一致（此前权重和
// 成交不变）。
func TestAuditLeakIsolationFutureReturns(t *testing.T) {
	base := auditLeakData(false)
	mutated := auditLeakData(true)

	spec := portfolioresearch.CombinationSpec{
		Method: portfolioresearch.CombinationRollingICWeight,
		RollingIC: &portfolioresearch.RollingICSpec{
			WindowYears: 1, Shrinkage: 0, MaxAbsWeight: 1, Fallback: portfolioresearch.CombinationFallbackEqualWeight,
		},
	}
	trainDates := base.dates[:8] // 训练窗 = 前 8 日；测试窗 = 第 9~11 日

	w1, cash1, err := resolveCombineWeights(spec, auditLeakFactors(base), trainDates, base.close)
	if err != nil {
		t.Fatalf("resolveCombineWeights(base): %v", err)
	}
	w2, cash2, err := resolveCombineWeights(spec, auditLeakFactors(mutated), trainDates, mutated.close)
	if err != nil {
		t.Fatalf("resolveCombineWeights(mutated): %v", err)
	}
	if cash1 != cash2 {
		t.Fatalf("现金 fallback 标记不一致: %v/%v", cash1, cash2)
	}
	if !reflect.DeepEqual(w1, w2) {
		t.Fatalf("测试窗未来收益变化后训练权重变化（泄漏）: base=%v mutated=%v", w1, w2)
	}
	if w1 == nil {
		t.Fatal("训练窗数据充足，滚动 IC 权重不应回退（nil）")
	}

	// 前 8 日分数与成交逐位一致（此前权重和成交不变）。
	scores1, err := combinePortfolio(auditLeakFactors(base), portfolioresearch.TransformMissingExclude, w1, cash1)
	if err != nil {
		t.Fatal(err)
	}
	scores2, err := combinePortfolio(auditLeakFactors(mutated), portfolioresearch.TransformMissingExclude, w2, cash2)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range base.dates[:8] {
		if !reflect.DeepEqual(scores1[d], scores2[d]) {
			t.Fatalf("测试窗未来收益变化后 %s 分数变化（泄漏）: %v vs %v", d, scores1[d], scores2[d])
		}
	}

	model := auditGoldenModel()
	ledgers1, _, err := runPortfolioDays(model, scores1, base, base.dates[:8], portfolioNotional, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ledgers2, _, err := runPortfolioDays(model, scores2, mutated, mutated.dates[:8], portfolioNotional, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ledgers1, ledgers2) {
		t.Fatalf("测试窗未来收益变化后前 8 日成交/现金/净值变化（泄漏）:\nbase=%+v\nmutated=%+v", ledgers1, ledgers2)
	}

	// 测试窗（第 9~11 日）收益确实不同 → 泄漏测试本身有效（对照组）。
	rets1 := returnsFor(base, base.dates[9])
	rets2 := returnsFor(mutated, mutated.dates[9])
	if reflect.DeepEqual(rets1, rets2) {
		t.Fatal("对照组无效：测试窗未来收益未真正变化")
	}
}
