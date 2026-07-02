package buy

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// A底部抬升 是底部抬升买入策略（只判断低点抬升，不要求高点抬升）。
// 通过识别最近 2 个低点(L1<L2)，要求 L2 > L1（底部在抬升），
// 且两点之间有满足间隔的高点作为结构确认。
// 适合捕捉底部抬升但高点尚未突破的趋势启动点。
//
// 字段说明：
//   - Window : 顶底判断窗口大小（前后各 Window 根 K 线），默认 8
type A底部抬升 struct {
	Window int
}

func (s A底部抬升) Name() string {
	w := s.Window
	if w == 0 {
		w = 8
	}
	return fmt.Sprintf("底部抬升(W%d)", w)
}

func (s A底部抬升) Buy(code string, dks extend.Klines) bool {
	s = s.withDefaults()
	if len(dks) < 60 {
		return false
	}

	highs, lows := findTrendUpPoints(dks, s.Window)
	if len(lows) < 2 {
		return false
	}

	l2, l1 := lows[0], lows[1]

	// 至少需要一个高点在 L1 和 L2 之间
	if len(highs) < 1 {
		return false
	}

	return isBottomUpShape(l1, l2, highs, s.Window)
}

func (s A底部抬升) withDefaults() A底部抬升 {
	if s.Window <= 0 {
		s.Window = 8
	}
	return s
}

// isBottomUpShape 判断底部抬升形态：
// L1 < L2（底部抬升），L1 和 L2 之间至少有一个高点，且间距 >= window。
func isBottomUpShape(l1, l2 trendUpPoint, highs []trendUpPoint, window int) bool {
	if l2.value <= l1.value {
		return false
	}
	if l2.index-l1.index < window {
		return false
	}

	// 至少有一个高点在 L1 和 L2 之间
	hasHighBetween := false
	for _, h := range highs {
		if h.index > l1.index && h.index < l2.index {
			hasHighBetween = true
			break
		}
	}
	return hasHighBetween
}

// Explain 实现 core.Explainer，返回 A底部抬升 的逐步判定原因。
func (s A底部抬升) Explain(code string, dks extend.Klines) []core.ExplainStep {
	s = s.withDefaults()
	steps := []core.ExplainStep{
		{
			Name:    "K线数量",
			Matched: len(dks) >= 60,
			Detail:  fmt.Sprintf("%d >= 60", len(dks)),
		},
	}
	if len(dks) < 60 {
		return steps
	}

	highs, lows := findTrendUpPoints(dks, s.Window)
	steps = append(steps, core.ExplainStep{
		Name:    "低点数量",
		Matched: len(lows) >= 2,
		Detail:  fmt.Sprintf("低点%d个，需要2个", len(lows)),
	})
	if len(lows) < 2 {
		return steps
	}

	l2, l1 := lows[0], lows[1]

	steps = append(steps, core.ExplainStep{
		Name:    "低点顺序",
		Matched: l1.index < l2.index,
		Detail:  fmt.Sprintf("L1=%s, L2=%s", trendUpPointDesc(dks, l1, false), trendUpPointDesc(dks, l2, false)),
	})

	steps = append(steps, core.ExplainStep{
		Name:    "低点间隔",
		Matched: l2.index-l1.index >= s.Window,
		Detail:  fmt.Sprintf("L2-L1=%d，需要 >= %d", l2.index-l1.index, s.Window),
	})

	steps = append(steps, core.ExplainStep{
		Name:    "底部抬升",
		Matched: l2.value > l1.value,
		Detail:  fmt.Sprintf("L2 %.2f > L1 %.2f", priceFloat(l2.value), priceFloat(l1.value)),
	})

	hasHighBetween := false
	for _, h := range highs {
		if h.index > l1.index && h.index < l2.index {
			hasHighBetween = true
			break
		}
	}
	steps = append(steps, core.ExplainStep{
		Name:    "中间高点",
		Matched: hasHighBetween && len(highs) >= 1,
		Detail:  fmt.Sprintf("高点%d个，L1-L2间存在高点=%v", len(highs), hasHighBetween),
	})

	return steps
}

// Annotate 实现 core.Visualizer，返回策略识别的关键高低点标注。
func (s A底部抬升) Annotate(code string, dks extend.Klines) []core.Annotation {
	s = s.withDefaults()
	highs, lows := findTrendUpPoints(dks, s.Window)
	if len(highs) > 2 {
		highs = highs[:2]
	}
	if len(lows) > 2 {
		lows = lows[:2]
	}

	anns := make([]core.Annotation, 0, len(highs)+len(lows))
	for i, h := range highs {
		label := "H2"
		if i == 1 {
			label = "H1"
		}
		anns = append(anns, core.Annotation{
			Index: h.index,
			Time:  dks[h.index].Time,
			Price: dks[h.index].High.Float64(),
			Label: label,
			Color: "#ef4444",
			Note:  fmt.Sprintf("高点 %.2f @ %s", dks[h.index].High.Float64(), dks[h.index].Time.Format("2006-01-02")),
		})
	}
	for i, l := range lows {
		label := "L2"
		if i == 1 {
			label = "L1"
		}
		anns = append(anns, core.Annotation{
			Index: l.index,
			Time:  dks[l.index].Time,
			Price: dks[l.index].Low.Float64(),
			Label: label,
			Color: "#22c55e",
			Note:  fmt.Sprintf("低点 %.2f @ %s", dks[l.index].Low.Float64(), dks[l.index].Time.Format("2006-01-02")),
		})
	}
	return anns
}
