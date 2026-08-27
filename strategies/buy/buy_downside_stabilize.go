package buy

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// ============================================================================
// 下跌企稳+缩量+倍量 策略
//
// 形态：个股长期下跌后开始企稳，成交量持续萎缩平稳，随后出现 3 倍倍量，
//       视为资金介入信号，买入后观察未来 N 天表现。
//
// 条件（全部同时满足）：
//   1. 长期下跌（可选三档，以倍量日之前的昨日收盘为基准）：
//      - A 深度回撤：距 120 日最高价回撤 >= MinDrawdown(40%)，且昨日收盘在 60 日均线下方
//      - B 温和回撤：距 120 日最高价回撤 >= MinDrawdown(25%)，且昨日收盘在 60 日均线下方
//      - C 不做下跌过滤（仅企稳+缩量+倍量）
//   2. 企稳信号：倍量日前近 N 日内出现过企稳形态（阳线+收盘走高+不创新低）
//   3. 缩量平稳：倍量日前近 5 日成交量持续萎缩且量能平稳（无剧烈波动）
//   4. 3 倍倍量（同时满足）：今日量 >= 昨日量 * VolumeRatio(3.0)
//                           且今日量 >= 近 5 日均量 * VolumeRatio(3.0)
// ============================================================================

// A下跌企稳倍量 是"长期下跌后企稳 + 缩量平稳 + 3倍倍量"的复合买入策略。
// 通过 Mode 选择下跌过滤档位：
//   Mode="deep"      深度回撤（默认），要求距120日高点回撤 >= MinDrawdown 且收盘在60日线下
//   Mode="mild"      温和回撤，要求距120日高点回撤 >= MinDrawdown 且收盘在60日线下
//   Mode="none"      不做长期下跌过滤（仅企稳+缩量+倍量）
//   Mode="confirm5"  不做下跌过滤，新增"倍量日收盘站上5日均线"右侧确认（缩量企稳后的首次放量站稳5日线）
// MinDrawdown 表示最小回撤幅度（百分比），deep 默认 40，mild 默认 25。
// VolumeRatio 表示倍量阈值，默认 3.0（较昨日量与近5日均量同时放大）。
// ShrinkDays 表示缩量观察窗口，默认 5。
type A下跌企稳倍量 struct {
	Mode        string
	MinDrawdown float64
	VolumeRatio float64
	ShrinkDays  int
	// TdxVolume 表示使用通达信标准"倍量"定义（今日量 > 昨日量 × 2，即 VOL/REF(VOL,1)>2），
	// 替代默认的"较昨日量与近5日均量同时放大 VolumeRatio 倍"双重确认。
	TdxVolume bool
}

func (b A下跌企稳倍量) Name() string {
	mode := b.mode()
	dd := b.drawdown()
	switch mode {
	case "deep":
		return fmt.Sprintf("下跌企稳倍量[深回撤%.0f%%+缩量+%.1f倍]", dd, b.volumeRatio())
	case "mild":
		return fmt.Sprintf("下跌企稳倍量[温回撤%.0f%%+缩量+%.1f倍]", dd, b.volumeRatio())
	case "confirm5":
		if b.TdxVolume {
			return "企稳倍量[站上5日线+缩量+通达信倍量(>2倍)]"
		}
		return fmt.Sprintf("企稳倍量[站上5日线+缩量+%.1f倍]", b.volumeRatio())
	default:
		return fmt.Sprintf("下跌企稳倍量[仅企稳+缩量+%.1f倍]", b.volumeRatio())
	}
}

func (b A下跌企稳倍量) mode() string {
	switch b.Mode {
	case "mild", "none", "confirm5":
		return b.Mode
	}
	return "deep"
}

func (b A下跌企稳倍量) drawdown() float64 {
	switch b.mode() {
	case "mild":
		if b.MinDrawdown > 0 {
			return b.MinDrawdown
		}
		return 25
	default:
		if b.MinDrawdown > 0 {
			return b.MinDrawdown
		}
		return 40
	}
}

func (b A下跌企稳倍量) volumeRatio() float64 {
	if b.VolumeRatio > 0 {
		return b.VolumeRatio
	}
	return 3.0
}

func (b A下跌企稳倍量) shrinkDays() int {
	if b.ShrinkDays > 0 {
		return b.ShrinkDays
	}
	return 5
}

func (b A下跌企稳倍量) Buy(code string, dks extend.Klines) bool {
	n := len(dks)
	if n < 130 { // 需要足够历史计算 120 日高点 + 60 日均线 + 企稳/缩量窗口
		return false
	}

	// 1. 长期下跌过滤（以倍量日之前的昨日收盘为基准，避免放量日突破影响判断）
	//    confirm5 档不要求下跌过滤（用户确认：去除此条件）
	mode := b.mode()
	if mode != "none" && mode != "confirm5" {
		dd := b.drawdown()
		high120 := dks[n-121 : n-1].HHV(120).Float64()
		if high120 <= 0 {
			return false
		}
		closePrice := dks[n-2].Close.Float64()
		drawdown := (high120 - closePrice) / high120 * 100
		if drawdown < dd {
			return false
		}
		// 昨日收盘在 60 日均线下方
		ma60 := core.MA(dks[:n-1], 60)
		if closePrice > ma60 {
			return false
		}
	}

	// 2. 企稳信号：倍量日之前的近 N 日（含昨日）出现过企稳形态（阳线+不创新低）
	if !stabilizeSignal(dks, b.shrinkDays()+2) {
		return false
	}

	// 3. 缩量平稳：倍量日之前的近 shrinkDays 日成交量持续萎缩且平稳
	if !shrinkStable(dks, b.shrinkDays()) {
		return false
	}

	// 4. 倍量（今日量 vs 昨日量，需严格大于阈值）
	//    通达信倍量阈值固定为 2（VOL/REF(VOL,1)>2）；默认档位用 VolumeRatio
	ratio := b.volumeRatio()
	if b.TdxVolume {
		ratio = 2.0
	}
	today := dks[n-1]
	yesterday := dks[n-2]
	if yesterday.Volume <= 0 {
		return false
	}
	ratioVsYesterday := float64(today.Volume) / float64(yesterday.Volume)
	if ratioVsYesterday <= ratio {
		return false
	}

	// 4b. 通达信倍量：仅要求今日量 > 昨日量 × 2（VOL/REF(VOL,1)>2）
	//     默认（非通达信）则额外要求今日量 >= 近5日均量 × VolumeRatio 双重确认
	if !b.TdxVolume {
		avg5 := core.AverageVolume(dks[n-1-b.shrinkDays() : n-1])
		if avg5 <= 0 || float64(today.Volume) < avg5*ratio {
			return false
		}
	}

	// 5. 右侧确认（仅 confirm5 档）：倍量日收盘站上 5 日均线
	if mode == "confirm5" {
		ma5 := core.MA(dks[n-6:n-1], 5) // 截止昨日的 5 日均线
		if today.Close.Float64() <= ma5 {
			return false
		}
	}

	return true
}

// stabilizeSignal 企稳信号：在最近 Window 日（不含今日）内出现过企稳形态。
// 企稳形态定义：当日阳线 + 收盘 > 前日收盘 + 当日最低 >= 再前 N 日最低（不创新低）。
func stabilizeSignal(dks extend.Klines, window int) bool {
	n := len(dks)
	if n < window+3 {
		return false
	}
	for i := n - window - 1; i <= n-2; i++ {
		today := dks[i]
		if today.Close <= today.Open {
			continue
		}
		if i < 1 {
			continue
		}
		yesterday := dks[i-1]
		if today.Close <= yesterday.Close {
			continue
		}
		// 当日最低 >= 前 3 日最低（不创新低）
		if i < 3 {
			continue
		}
		prevLow := dks[i-3 : i].LLV(3)
		if today.Low < prevLow {
			continue
		}
		return true
	}
	return false
}

// shrinkStable 缩量平稳：近 ShrinkDays 日成交量持续萎缩且量能平稳。
// 要求：日均量不高于前一段均量，且量能波动小（最大/最小 <= 2 倍），
// 且今日量 <= 近 ShrinkDays 日均量（当日仍处于缩量状态）。
func shrinkStable(dks extend.Klines, days int) bool {
	n := len(dks)
	if n < days*2+1 {
		return false
	}
	// 近期窗口 [n-days, n-1)
	recent := dks[n-1-days : n-1]
	// 前期窗口 [n-2*days, n-days)
	prior := dks[n-1-2*days : n-1-days]

	recentAvg := core.AverageVolume(recent)
	priorAvg := core.AverageVolume(prior)
	if priorAvg <= 0 {
		return false
	}
	// 成交量较前期萎缩
	if recentAvg >= priorAvg {
		return false
	}

	// 量能平稳：近期窗口内最大/最小成交量 <= 2 倍（排除忽大忽小）
	minVol, maxVol := recent[0].Volume, recent[0].Volume
	for _, k := range recent {
		if k.Volume < minVol {
			minVol = k.Volume
		}
		if k.Volume > maxVol {
			maxVol = k.Volume
		}
	}
	if minVol <= 0 || float64(maxVol)/float64(minVol) > 2 {
		return false
	}

	return true
}
