package buy

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

func TestFindTrendUpPointsReturnsLatestTwoHighsAndLows(t *testing.T) {
	ks := makeTrendUp连续K线(50)
	setTrendUpPoint(ks, 10, 12, 10)
	setTrendUpPoint(ks, 22, 9, 7)
	setTrendUpPoint(ks, 34, 15, 13)
	setTrendUpPoint(ks, 46, 10.3, 9)
	setTrendUpPoint(ks, 47, 10.2, 10)
	setTrendUpPoint(ks, 48, 10, 9.5)

	highs, lows := findTrendUpPoints(ks, 4)

	if len(highs) < 2 || len(lows) < 2 {
		t.Fatalf("expected at least two highs and lows, got highs=%d lows=%d", len(highs), len(lows))
	}
	if highs[0].index != 34 || highs[1].index != 10 {
		t.Fatalf("expected latest highs at 34 and 10, got %d and %d", highs[0].index, highs[1].index)
	}
	if lows[0].index != 46 || lows[1].index != 22 {
		t.Fatalf("expected latest lows at 46 and 22, got %d and %d", lows[0].index, lows[1].index)
	}
}

func TestFindTrendUpPointsUsesOneRightBarAtEnd(t *testing.T) {
	ks := makeTrendUp连续K线(20)
	setTrendUpPoint(ks, 18, 12, 11)
	setTrendUpPoint(ks, 19, 11, 10)

	highs, _ := findTrendUpPoints(ks, 8)

	if len(highs) == 0 {
		t.Fatal("expected a high point near the right edge")
	}
	if highs[0].index != 18 {
		t.Fatalf("expected right-edge high at 18, got %d", highs[0].index)
	}
}

func TestFindTrendUpPointsRejectsAdjacentHighs(t *testing.T) {
	ks := makeTrendUp连续K线(30)
	// 两个相邻的高点，window=4 时不应同时收录
	setTrendUpPoint(ks, 20, 15, 9)
	setTrendUpPoint(ks, 19, 14, 9)

	highs, _ := findTrendUpPoints(ks, 4)

	if len(highs) < 1 {
		t.Fatal("expected at least one high")
	}
	if len(highs) >= 2 && highs[1].index == 19 {
		t.Fatalf("adjacent high at 19 should not be collected alongside 20, got highs=%v", highs)
	}
}

func TestAnnotateReturnsLabelsAndColors(t *testing.T) {
	ks := makeTrendUp连续K线(50)
	setTrendUpPoint(ks, 10, 12, 10)
	setTrendUpPoint(ks, 22, 9, 7)
	setTrendUpPoint(ks, 34, 15, 13)
	setTrendUpPoint(ks, 46, 10.3, 9)
	setTrendUpPoint(ks, 47, 10.2, 10)
	setTrendUpPoint(ks, 48, 10, 9.5)

	s := A底顶部抬升{Window: 4, MaxGainMultiple: 5}
	anns := s.Annotate("sz000988", ks)

	if len(anns) != 4 {
		t.Fatalf("expected 4 annotations (2 highs + 2 lows), got %d", len(anns))
	}

	labels := map[string]bool{}
	for _, a := range anns {
		labels[a.Label] = true
	}
	for _, want := range []string{"H1", "H2", "L1", "L2"} {
		if !labels[want] {
			t.Errorf("missing label %s", want)
		}
	}
}

func TestExplainReturnsReadableRuleSteps(t *testing.T) {
	ks := makeTrendUp连续K线(60)
	setTrendUpPoint(ks, 10, 12, 10)
	setTrendUpPoint(ks, 22, 9, 7)
	setTrendUpPoint(ks, 34, 15, 13)
	setTrendUpPoint(ks, 46, 10.3, 9)
	setTrendUpPoint(ks, 47, 10.2, 10)
	setTrendUpPoint(ks, 48, 10, 9.5)

	steps := (A底顶部抬升{Window: 4, MaxGainMultiple: 5}).Explain("sz000988", ks)

	if len(steps) == 0 {
		t.Fatal("expected explain steps")
	}
	for _, name := range []string{"K线数量", "关键点", "时间顺序", "间隔", "低点抬升", "高点抬升", "涨幅平衡"} {
		if !hasExplainStep(steps, name) {
			t.Fatalf("expected explain step %s, got %#v", name, steps)
		}
	}
}

func TestExplainShowsFailedReasonDetail(t *testing.T) {
	ks := makeTrendUp连续K线(20)

	steps := (A底顶部抬升{Window: 4, MaxGainMultiple: 5}).Explain("sz000988", ks)

	for _, step := range steps {
		if step.Matched {
			continue
		}
		if step.Detail == "" {
			t.Fatalf("failed step %s should include detail", step.Name)
		}
		return
	}
	t.Fatal("expected at least one failed explain step")
}

func hasExplainStep(steps []core.ExplainStep, name string) bool {
	for _, step := range steps {
		if step.Name == name {
			return true
		}
	}
	return false
}

func makeTrendUp连续K线(n int) extend.Klines {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	ks := make(extend.Klines, n)
	for i := 0; i < n; i++ {
		price := 10 + float64(i)/100
		ks[i] = &extend.Kline{
			Unix: base.AddDate(0, 0, i).Unix(),
			Kline: &protocol.Kline{
				Time:   base.AddDate(0, 0, i),
				Open:   protocol.Yuan(price),
				Close:  protocol.Yuan(price),
				High:   protocol.Yuan(price),
				Low:    protocol.Yuan(price),
				Volume: 100,
			},
		}
	}
	return ks
}

func setTrendUpPoint(ks extend.Klines, index int, high, low float64) {
	ks[index].High = protocol.Yuan(high)
	ks[index].Low = protocol.Yuan(low)
}
