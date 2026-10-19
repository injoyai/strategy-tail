package lab

import (
	"math"
	"testing"
)

func TestPracticeReportAccountAndFees(t *testing.T) {
	m := practiceTestMarket(t, []float64{10, 10, 12, 11, 999})
	s := practiceTestSession(m, 0)
	s.Phase = practicePhaseFinished
	s.Stage = practiceStageSold
	s.CurrentIndex = 3
	s.BuyIndex = 1
	s.SellIndex = 3
	s.Quantity = 100
	s.BuyCostYuan = 1005
	s.SellIncomeYuan = 1094
	v := practiceReviewViewOf(s, m)
	r := v.Report
	if r == nil || !r.Traded || r.Status != "sold" || r.Fees != 11 || r.Cash != 20089 || r.PositionValue != 0 || r.Equity != 20089 {
		t.Fatalf("report=%+v", r)
	}
	if *r.AccountPct != 89.0/20000*100 || *r.WaitDays != 1 || len(r.Points) != 4 {
		t.Fatalf("report=%+v", r)
	}
	if *r.MaxDrawdown != 106 || math.Abs(*r.MaxDrawdownPct-106.0/20195*100) > 1e-10 {
		t.Fatalf("drawdown=%v pct=%v", *r.MaxDrawdown, *r.MaxDrawdownPct)
	}
	if v.Eval.RealizedPnL != 89 || v.Eval.HoldingDays != 2 {
		t.Fatalf("eval=%+v", v.Eval)
	}
	// 复盘中的未来价格可以显示，但不能改变报告资金和回撤。
	m.Days[4].Close = 1
	again := practiceReviewViewOf(s, m).Report
	if again.Equity != r.Equity || *again.MaxDrawdown != *r.MaxDrawdown || again.EndDate != m.Days[3].Date {
		t.Fatal("报告受到结束后行情影响")
	}
}

func TestPracticeReportOpenAndUntraded(t *testing.T) {
	m := practiceTestMarket(t, []float64{10, 12, 999})
	s := practiceTestSession(m, 0)
	s.Phase = practicePhaseFinished
	s.CurrentIndex = 1
	r := practiceReviewViewOf(s, m).Report
	if r.Traded || r.AccountPct != nil || r.MaxDrawdownPct != nil || r.WaitDays != nil || len(r.Points) != 0 {
		t.Fatalf("未交易不得冒充零收益成交: %+v", r)
	}
	s.Stage = practiceStageHolding
	s.BuyIndex = 0
	s.Quantity = 100
	s.BuyCostYuan = 1005
	v := practiceReviewViewOf(s, m)
	r = v.Report
	if r.Status != "open" || r.Fees != 5 || r.PositionValue != 1200 || r.Cash != 18995 || r.Equity != 20195 || v.Eval.OpenPnL != 195 {
		t.Fatalf("未平仓报告=%+v eval=%+v", r, v.Eval)
	}
	s.Phase = practicePhaseActive
	if practiceReportOf(s, m, nil) != nil {
		t.Fatal("未结束前不得生成报告")
	}
}
