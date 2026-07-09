package core_test

import (
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
