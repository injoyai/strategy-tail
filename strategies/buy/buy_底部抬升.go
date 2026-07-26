package buy

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// A底顶部抬升 是上升趋势买入策略。
// 通过识别最近 2 个高点(H1<H2)和 2 个低点(L1<L2)，
// 要求时间顺序为 H1 -> L1 -> H2 -> L2，且高点、低点同步抬升。
// 适合捕捉震荡上行趋势中的回调低点。
//
// 字段说明：
//   - Window          : 顶底判断窗口大小（前后各 Window 根 K 线），默认 8
//   - MaxGainMultiple : 高点涨幅与低点涨幅的最大差距，默认 5%
type A底顶部抬升 struct {
	Window          int
	MaxGainMultiple float64
}

type trendUpPoint struct {
	index int
	value int64 // 用 int64 比较，单位为厘
}

func (s A底顶部抬升) Name() string {
	s = s.withDefaults()
	return fmt.Sprintf("顶底抬升(W%d·涨幅%.1f%%)", s.Window, s.MaxGainMultiple)
}

func (s A底顶部抬升) Buy(code string, dks extend.Klines) bool {
	s = s.withDefaults()
	if len(dks) < 60 {
		return false
	}

	highs, lows := findTrendUpPoints(dks, s.Window)
	if len(highs) < 2 || len(lows) < 2 {
		return false
	}

	// 倒序遍历：[0] 最新, [1] 次新
	h2, h1 := highs[0], highs[1]
	l2, l1 := lows[0], lows[1]

	return isTrendUpShape(h1, l1, h2, l2, s.Window) &&
		isBalancedTrendUpGain(h1, l1, h2, l2, s.MaxGainMultiple)
}

func (s A底顶部抬升) withDefaults() A底顶部抬升 {
	if s.Window <= 0 {
		s.Window = 12
	}
	if s.MaxGainMultiple <= 0 {
		s.MaxGainMultiple = 20
	}
	return s
}

func findTrendUpPoints(ks extend.Klines, window int) (highs, lows []trendUpPoint) {
	startIdx := len(ks) - 1 - 1
	if startIdx < window {
		startIdx = window
	}

	// nextHighMinIdx / nextLowMinIdx：找到极值点后，下一个同类极值点至少要隔 window 根K线
	nextHighMinIdx := startIdx
	nextLowMinIdx := startIdx

	for i := startIdx; i >= window; i-- {
		if len(highs) >= 2 && len(lows) >= 2 {
			break
		}

		currentHigh := int64(ks[i].High)
		currentLow := int64(ks[i].Low)
		isHigh, isLow := isTrendUpPoint(ks, i, window, currentHigh, currentLow)

		if isHigh && len(highs) < 2 && i <= nextHighMinIdx {
			highs = append(highs, trendUpPoint{i, currentHigh})
			nextHighMinIdx = i - window
		}
		if isLow && len(lows) < 2 && i <= nextLowMinIdx {
			lows = append(lows, trendUpPoint{i, currentLow})
			nextLowMinIdx = i - window
		}
	}

	return highs, lows
}

func isTrendUpPoint(ks extend.Klines, index, window int, currentHigh, currentLow int64) (isHigh, isLow bool) {
	isHigh = true
	isLow = true

	for j := index - window; j < index; j++ {
		if int64(ks[j].High) > currentHigh {
			isHigh = false
		}
		if int64(ks[j].Low) < currentLow {
			isLow = false
		}
	}

	if !isHigh && !isLow {
		return false, false
	}

	rightEnd := index + window
	if rightEnd >= len(ks) {
		rightEnd = len(ks) - 1
	}
	for j := index + 1; j <= rightEnd; j++ {
		if int64(ks[j].High) > currentHigh {
			isHigh = false
		}
		if int64(ks[j].Low) < currentLow {
			isLow = false
		}
	}

	return isHigh, isLow
}

func isTrendUpShape(h1, l1, h2, l2 trendUpPoint, window int) bool {
	if !(h1.index < l1.index && l1.index < h2.index && h2.index < l2.index) {
		return false
	}
	if l1.index-h1.index < window || h2.index-l1.index < window || l2.index-h2.index < window {
		return false
	}
	if l2.value <= l1.value || h2.value <= h1.value {
		return false
	}
	return l1.value < h1.value && l2.value < h2.value
}

func isBalancedTrendUpGain(h1, l1, h2, l2 trendUpPoint, maxGainMultiple float64) bool {
	if h1.value == 0 || l1.value == 0 {
		return false
	}

	hGain := float64(h2.value-h1.value) / float64(h1.value)
	lGain := float64(l2.value-l1.value) / float64(l1.value)
	return hGain <= lGain*maxGainMultiple && lGain <= hGain*maxGainMultiple
}

// Explain 实现 core.Explainer，返回 A底顶部抬升 的逐步判定原因。
func (s A底顶部抬升) Explain(code string, dks extend.Klines) []core.ExplainStep {
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
		Name:    "关键点",
		Matched: len(highs) >= 2 && len(lows) >= 2,
		Detail:  fmt.Sprintf("高点%d个，低点%d个，需要各2个", len(highs), len(lows)),
	})
	if len(highs) < 2 || len(lows) < 2 {
		return steps
	}

	h2, h1 := highs[0], highs[1]
	l2, l1 := lows[0], lows[1]

	steps = append(steps,
		core.ExplainStep{
			Name:    "时间顺序",
			Matched: h1.index < l1.index && l1.index < h2.index && h2.index < l2.index,
			Detail:  fmt.Sprintf("H1=%s, L1=%s, H2=%s, L2=%s", trendUpPointDesc(dks, h1, true), trendUpPointDesc(dks, l1, false), trendUpPointDesc(dks, h2, true), trendUpPointDesc(dks, l2, false)),
		},
		core.ExplainStep{
			Name:    "间隔",
			Matched: l1.index-h1.index >= s.Window && h2.index-l1.index >= s.Window && l2.index-h2.index >= s.Window,
			Detail:  fmt.Sprintf("L1-H1=%d, H2-L1=%d, L2-H2=%d，需要 >= %d", l1.index-h1.index, h2.index-l1.index, l2.index-h2.index, s.Window),
		},
		core.ExplainStep{
			Name:    "低点抬升",
			Matched: l2.value > l1.value,
			Detail:  fmt.Sprintf("L2 %.2f > L1 %.2f", priceFloat(l2.value), priceFloat(l1.value)),
		},
		core.ExplainStep{
			Name:    "高点抬升",
			Matched: h2.value > h1.value,
			Detail:  fmt.Sprintf("H2 %.2f > H1 %.2f", priceFloat(h2.value), priceFloat(h1.value)),
		},
		core.ExplainStep{
			Name:    "高低点关系",
			Matched: l1.value < h1.value && l2.value < h2.value,
			Detail:  fmt.Sprintf("L1 %.2f < H1 %.2f，L2 %.2f < H2 %.2f", priceFloat(l1.value), priceFloat(h1.value), priceFloat(l2.value), priceFloat(h2.value)),
		},
	)

	hGain := 0.0
	lGain := 0.0
	if h1.value != 0 {
		hGain = float64(h2.value-h1.value) / float64(h1.value)
	}
	if l1.value != 0 {
		lGain = float64(l2.value-l1.value) / float64(l1.value)
	}
	steps = append(steps, core.ExplainStep{
		Name:    "涨幅平衡",
		Matched: isBalancedTrendUpGain(h1, l1, h2, l2, s.MaxGainMultiple),
		Detail:  fmt.Sprintf("高点涨幅 %.2f%%，低点涨幅 %.2f%%，允许差距 %.1f 倍", hGain*100, lGain*100, s.MaxGainMultiple),
	})

	return steps
}

func trendUpPointDesc(dks extend.Klines, p trendUpPoint, high bool) string {
	price := dks[p.index].Low.Float64()
	if high {
		price = dks[p.index].High.Float64()
	}
	return fmt.Sprintf("%s %.2f", dks[p.index].Time.Format("2006-01-02"), price)
}

func priceFloat(value int64) float64 {
	return float64(value) / 1000
}

// Annotate 实现 core.Visualizer，返回策略识别的关键高低点标注。
// 高点标为 H2(最新)/H1(次新) 红色，低点标为 L2(最新)/L1(次新) 绿色。
func (s A底顶部抬升) Annotate(code string, dks extend.Klines) []core.Annotation {
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
