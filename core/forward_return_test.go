package core_test

import (
	"math"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

func TestDefaultForwardDays(t *testing.T) {
	days := core.DefaultForwardDays()
	expected := []int{1, 3, 5, 10, 15, 20, 30}
	if len(days) != len(expected) {
		t.Fatalf("expected %d days, got %d", len(expected), len(days))
	}
	for i, d := range days {
		if d != expected[i] {
			t.Fatalf("days[%d]: expected %d, got %d", i, expected[i], d)
		}
	}
}

func TestScanForwardReturns(t *testing.T) {
	// 构造5天K线,收盘价 10,11,12,13,14
	base := time.Date(2024, 1, 2, 15, 0, 0, 0, time.Local)
	dks := make(extend.Klines, 5)
	for i := 0; i < 5; i++ {
		dks[i] = testKline(base.AddDate(0, 0, i), 10, 10, 10, 10+float64(i))
	}

	bt := core.ForwardReturnAnalysis{
		Buyer:       alwaysBuyer{},
		ForwardDays: []int{1, 3},
	}

	results := bt.Scan("test", nil, dks)

	// alwaysBuyer 每天都触发,5天 = 5个信号
	if len(results) != 5 {
		t.Fatalf("expected 5 signals, got %d", len(results))
	}

	// 第1个信号: i=0, buyPrice=10
	// N=1: dks[1].Close=11, return = (11-10)/10*100 = 10%
	// N=3: dks[3].Close=13, return = (13-10)/10*100 = 30%
	r0 := results[0]
	if r0.BuyPrice.Float64() != 10 {
		t.Fatalf("buyPrice: expected 10, got %v", r0.BuyPrice)
	}
	if r0.Returns[1] != 10.0 {
		t.Fatalf("N=1 return: expected 10.0, got %v", r0.Returns[1])
	}
	if r0.Returns[3] != 30.0 {
		t.Fatalf("N=3 return: expected 30.0, got %v", r0.Returns[3])
	}

	// 最后一个信号: i=4, N=1 超出范围,不应有该N的记录
	r4 := results[4]
	if _, ok := r4.Returns[1]; ok {
		t.Fatal("last signal should not have N=1 return (out of range)")
	}
	if _, ok := r4.Returns[3]; ok {
		t.Fatal("last signal should not have N=3 return (out of range)")
	}
}

func TestSummarizeForwardReturns(t *testing.T) {
	returns := []core.ForwardReturn{
		{Returns: map[int]float64{1: 10.0, 3: 20.0}},
		{Returns: map[int]float64{1: -5.0, 3: 30.0}},
		{Returns: map[int]float64{1: 0.0, 3: -10.0}},
	}

	summaries := core.SummarizeForwardReturns(returns, []int{1, 3})

	if len(summaries) != 2 {
		t.Fatalf("expected 2 summaries, got %d", len(summaries))
	}

	// N=1: values = [10, -5, 0], avg = 5/3 ≈ 1.667, win=1, winRate=33.33%
	s1 := summaries[0]
	if s1.Days != 1 {
		t.Fatalf("days: expected 1, got %d", s1.Days)
	}
	if s1.Count != 3 {
		t.Fatalf("count: expected 3, got %d", s1.Count)
	}
	if math.Abs(s1.AvgReturn-1.6667) > 0.01 {
		t.Fatalf("avgReturn: expected ~1.6667, got %v", s1.AvgReturn)
	}
	if math.Abs(s1.WinRate-33.33) > 0.1 {
		t.Fatalf("winRate: expected ~33.33, got %v", s1.WinRate)
	}
	if s1.MaxReturn != 10.0 {
		t.Fatalf("maxReturn: expected 10.0, got %v", s1.MaxReturn)
	}
	if s1.MinReturn != -5.0 {
		t.Fatalf("minReturn: expected -5.0, got %v", s1.MinReturn)
	}
	// median of [10, -5, 0] sorted = [-5, 0, 10], median = 0
	if s1.MedianReturn != 0.0 {
		t.Fatalf("medianReturn: expected 0.0, got %v", s1.MedianReturn)
	}

	// N=3: values = [20, 30, -10], avg = 40/3 ≈ 13.33, win=2, winRate=66.67%
	s3 := summaries[1]
	if s3.Days != 3 {
		t.Fatalf("days: expected 3, got %d", s3.Days)
	}
	if math.Abs(s3.AvgReturn-13.333) > 0.01 {
		t.Fatalf("avgReturn: expected ~13.333, got %v", s3.AvgReturn)
	}
	if math.Abs(s3.WinRate-66.67) > 0.1 {
		t.Fatalf("winRate: expected ~66.67, got %v", s3.WinRate)
	}
	// median of [20, 30, -10] sorted = [-10, 20, 30], median = 20
	if s3.MedianReturn != 20.0 {
		t.Fatalf("medianReturn: expected 20.0, got %v", s3.MedianReturn)
	}
}

func TestSummarizeForwardReturnsEmpty(t *testing.T) {
	summaries := core.SummarizeForwardReturns(nil, []int{1, 3})
	if len(summaries) != 2 {
		t.Fatalf("expected 2 summaries, got %d", len(summaries))
	}
	for _, s := range summaries {
		if s.Count != 0 {
			t.Fatalf("expected count 0, got %d", s.Count)
		}
	}
}
