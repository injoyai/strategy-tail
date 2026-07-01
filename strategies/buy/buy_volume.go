package buy

import (
	"fmt"
	"sort"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

// A通达信倍量 是按通达信"倍量"公式翻写的买入策略。
// 默认严格执行 XG:SPK，即 TJ1、TJ2、TJ3、TJ4。
// Ratio 表示倍量阈值，默认 2.9。
// UseMore 可选择是否把公式中定义但未参与 XG 的额外条件加入过滤。
// 适合需要完整通达信"倍量"形态的场景，配合 buy.Strategy 包装可命名识别。
type A通达信倍量 struct {
	Ratio   float64
	UseMore bool
}

func (b A通达信倍量) Name() string {
	return "通达信倍量"
}

func (b A通达信倍量) Buy(code string, dks extend.Klines) bool {
	ratio := b.Ratio
	if ratio == 0 {
		ratio = 2.9
	}

	n := len(dks)
	if n < 21 {
		return false
	}
	today := dks[n-1]
	yesterday := dks[n-2]
	if yesterday.Volume <= 0 {
		return false
	}

	TJ1 := float64(today.Volume)/float64(yesterday.Volume) >= ratio
	TJ2 := today.Close > yesterday.Close && today.High > today.Close && today.High == dks.HHV(6)
	TJ3 := dks[n-11:n-1].LLV(10).Float64() >= dks[n-11:n-1].HHV(10).Float64()*0.8
	TJ4 := dks.LLV(5) > dks.LLV(20)

	if !(TJ1 && TJ2 && TJ3 && TJ4) {
		return false
	}

	if !b.UseMore {
		return true
	}

	if countDoubleVolume(dks, 60, ratio) <= 1 {
		return false
	}

	idx := previousDoubleVolumeIndex(dks, ratio)
	if idx < 0 {
		return false
	}
	prev := dks[idx]
	if today.Volume <= prev.Volume {
		return false
	}
	if !(today.Close > prev.Close && today.Close > prev.Open) {
		return false
	}

	return true
}

func countDoubleVolume(dks extend.Klines, days int, ratio float64) int {
	if days > len(dks) {
		days = len(dks)
	}
	count := 0
	start := len(dks) - days
	if start < 1 {
		start = 1
	}
	for i := start; i < len(dks); i++ {
		if dks[i-1].Volume > 0 && float64(dks[i].Volume)/float64(dks[i-1].Volume) >= ratio {
			count++
		}
	}
	return count
}

func previousDoubleVolumeIndex(dks extend.Klines, ratio float64) int {
	for i := len(dks) - 2; i >= 1; i-- {
		if dks[i-1].Volume > 0 && float64(dks[i].Volume)/float64(dks[i-1].Volume) >= ratio {
			return i
		}
	}
	return -1
}

// A倍量 是判断"基准成交量 × MinRatio <= 今日成交量 <= 基准成交量 × MaxRatio"的买入条件。
// MinRatio 表示最小倍量阈值，默认 2.0。
// MaxRatio 表示最大倍量阈值，0 表示不限。
// BaseDays 表示基准成交量的统计天数，默认 1（即昨日单日成交量）。
//   - BaseDays=1: 基准 = 昨日量
//   - BaseDays=5: 基准 = 近5日平均量
//
// 不附带价格、涨幅、形态等附加条件，适合作为最基础的量能放大筛选。
type A倍量 struct {
	MinRatio float64
	MaxRatio float64
	BaseDays int
}

func (b A倍量) Name() string {
	min := b.MinRatio
	if min == 0 {
		min = 2
	}
	max := b.MaxRatio
	base := b.BaseDays
	if base == 0 {
		base = 1
	}
	var ratioDesc string
	if max > 0 && max > min {
		ratioDesc = fmt.Sprintf("倍量%.1f~%.1f", min, max)
	} else {
		ratioDesc = fmt.Sprintf("倍量%.1f", min)
	}
	if base == 1 {
		return ratioDesc
	}
	return fmt.Sprintf("%s·%d日均", ratioDesc, base)
}

func (b A倍量) Buy(code string, dks extend.Klines) bool {
	min := b.MinRatio
	if min == 0 {
		min = 2
	}
	max := b.MaxRatio
	base := b.BaseDays
	if base == 0 {
		base = 1
	}
	n := len(dks)
	if n < base+1 {
		return false
	}
	today := dks[n-1]
	var baseline float64
	if base == 1 {
		baseline = float64(dks[n-2].Volume)
	} else {
		sum := 0.0
		for i := n - 1 - base; i < n-1; i++ {
			sum += float64(dks[i].Volume)
		}
		baseline = sum / float64(base)
	}
	if baseline <= 0 {
		return false
	}
	actualRatio := float64(today.Volume) / baseline
	if actualRatio < min {
		return false
	}
	if max > 0 && max > min && actualRatio > max {
		return false
	}
	return true
}

// BuyCloseAboveMA 是收盘价站上指定均线的买入条件。
// Period 表示均线周期，默认 20。
// 当最新收盘价大于指定周期均线时返回买入信号。
// 适合作为趋势过滤条件与其它买入策略放入 BuyAll 组合使用。
type BuyCloseAboveMA struct {
	Period int
}

func (b BuyCloseAboveMA) Name() string {
	return fmt.Sprintf("收盘高于%d日均线", b.Period)
}

func (b BuyCloseAboveMA) Buy(code string, dks extend.Klines) bool {
	if b.Period == 0 {
		b.Period = 20
	}
	if len(dks) < b.Period {
		return false
	}

	today := dks[len(dks)-1]
	ma := core.MA(dks, b.Period)
	return today.Close.Float64() > ma
}

// A现价大于N日均线 是当天价格高于指定均线的买入条件。
// Period 表示均线周期，默认 20。
// 当最新交易日的价格高于指定周期均线时返回买入信号。
// PriceField 表示使用哪一个价格字段，支持 open、close、high、low，默认 close。
// 适合表达“当天价格运行在某条均线上方”的过滤条件。
type A现价大于N日均线 int

func (b A现价大于N日均线) Name() string {
	return fmt.Sprintf("现价高于%d日均线", b)
}

func (b A现价大于N日均线) Buy(code string, dks extend.Klines) bool {
	day := int(b)
	if day == 0 {
		day = 20
	}
	if len(dks) < day {
		return false
	}

	today := dks[len(dks)-1]
	ma := core.MA(dks, day)

	return today.Close.Float64() > ma
}

// BuyCloseBelowMA 是收盘价低于指定均线的买入条件。
// Period 表示均线周期，默认 60。
// 当最新收盘价低于指定周期均线时返回买入信号。
// 适合表达“价格回到长期均线下方时尝试低吸”的过滤条件。
type BuyCloseBelowMA struct {
	Period int
}

func (b BuyCloseBelowMA) Name() string {
	return fmt.Sprintf("收盘低于%d日均线", b.Period)
}

func (b BuyCloseBelowMA) Buy(code string, dks extend.Klines) bool {
	if b.Period == 0 {
		b.Period = 60
	}
	if len(dks) < b.Period {
		return false
	}

	today := dks[len(dks)-1]
	ma := core.MA(dks, b.Period)
	return today.Close.Float64() < ma
}

// BreakMA 是突破均线买入条件。
// Period 表示均线周期，默认 20。
// 要求昨天收盘价还在均线下方，今天收盘价突破到均线上方。
// 适合表达“刚刚重新站回均线”的信号。
type BreakMA struct {
	Period int
}

func (b BreakMA) Name() string {
	return "突破均线"
}

func (b BreakMA) Buy(code string, dks extend.Klines) bool {
	if b.Period == 0 {
		b.Period = 20
	}
	if len(dks) < b.Period+1 {
		return false
	}

	n := len(dks)
	today := dks[n-1]
	yesterday := dks[n-2]
	maNow := core.MA(dks, b.Period)
	maPrev := core.MA(dks[:n-1], b.Period)
	if yesterday.Close.Float64() >= maPrev || today.Close.Float64() <= maNow {
		return false
	}

	return true
}

// MAUp 是均线向上买入条件。
// Period 表示均线周期，默认 20。
// Lookback 表示与多少个交易日前的均线值比较，默认 1。
// MinSlope 表示每一步均线相对前值的最小涨速，默认 0。
// 当当前均线值大于 Lookback 天前的均线值，且每一步上涨幅度都不低于 MinSlope 时返回买入信号。
// 适合过滤均线趋势方向，并排除涨速接近 0 的“走平式上涨”。
type MAUp struct {
	Period   int
	Lookback int
	MinSlope float64
}

func (b MAUp) Name() string {
	return fmt.Sprintf("%d日均线向上", b.Period)
}

func (b MAUp) Buy(code string, dks extend.Klines) bool {
	if b.Period == 0 {
		b.Period = 20
	}
	if b.Lookback == 0 {
		b.Lookback = 5
	}
	return maUp(dks, b.Period, b.Lookback, b.MinSlope)
}

func maUp(dks extend.Klines, period, lookback int, minSlope float64) bool {
	if period <= 0 || lookback <= 0 || len(dks) < period+lookback {
		return false
	}

	n := len(dks)
	for x := 0; x < lookback; x++ {
		maNow := core.MA(dks[:n-x], period)
		maPrev := core.MA(dks[:n-x-1], period)
		if maNow <= maPrev {
			return false
		}
		if maPrev <= 0 {
			return false
		}
		slope := (maNow - maPrev) / maPrev
		if slope < minSlope {
			return false
		}
	}
	return true
}

func maUp2(dks protocol.Klines, period, lookback int, minSlope float64) bool {
	if period <= 0 || lookback <= 0 || len(dks) < period+lookback {
		return false
	}

	n := len(dks)
	for x := 0; x < lookback; x++ {
		maNow := dks[:n-x].MA(period).Float64()
		maPrev := dks[:n-x-1].MA(period).Float64()
		if maNow <= maPrev {
			return false
		}
		if maPrev <= 0 {
			return false
		}
		slope := (maNow - maPrev) / maPrev
		if slope < minSlope {
			return false
		}
	}
	return true
}

// VolumeShrink 是缩量买入条件。
// Period 表示对比的前 N 日成交量均值，默认 5。
// Ratio 表示今日成交量必须低于前 N 日均量的比例，默认 0.8。
// 例如 Ratio=0.8 表示今日成交量小于前 5 日均量的 80%。
// 常用于“回调缩量”或“整理缩量”的组合过滤。
type VolumeShrink struct {
	Days  int
	Ratio float64
}

func (b VolumeShrink) Name() string {
	return "缩量"
}

func (b VolumeShrink) Buy(code string, dks extend.Klines) bool {
	if b.Days == 0 {
		b.Days = 5
	}
	if b.Ratio == 0 {
		b.Ratio = 0.8
	}
	if len(dks) < b.Days+1 {
		return false
	}

	today := dks[len(dks)-1]
	avg := core.AverageVolume(dks[len(dks)-1-b.Days : len(dks)-1])
	if avg <= 0 || float64(today.Volume) >= avg*b.Ratio {
		return false
	}

	return true
}

//// BuyVolumeExpand 是放量买入条件。
//// Period 表示对比的前 N 日成交量均值，默认 5。
//// Ratio 表示今日成交量必须高于前 N 日均量的倍数，默认 1.5。
//// 例如 Ratio=1.5 表示今日成交量大于前 5 日均量的 1.5 倍。
//// 常用于突破、启动、放量上涨等组合条件。
//type BuyVolumeExpand struct {
//	Period int
//	Ratio  float64
//}
//
//func (b BuyVolumeExpand) Name() string {
//	return "放量"
//}
//
//func (b BuyVolumeExpand) Buy(code string, dks extend.Klines, mks protocol.Klines) *core.Buy {
//	if b.Period == 0 {
//		b.Period = 5
//	}
//	if b.Ratio == 0 {
//		b.Ratio = 1.5
//	}
//	if len(dks) < b.Period+1 {
//		return nil
//	}
//
//	today := dks[len(dks)-1]
//	avg := core.AverageVolume(dks[len(dks)-1-b.Period : len(dks)-1])
//	if avg <= 0 || float64(today.Volume) <= avg*b.Ratio {
//		return nil
//	}
//
//	return &core.Buy{Code: code, Time: today.Time, Price: today.Close}
//}
//
//// BuyRiseRateRange 是日涨幅区间买入条件。
//// Min 表示最小涨幅，单位为百分比。
//// Max 表示最大涨幅，单位为百分比；Max 为 0 时不限制最大涨幅。
//// 例如 Min=0、Max=3 表示当天涨幅需要在 0% 到 3% 之间。
//// 适合过滤过弱或过强的当日 K 线。
//type BuyRiseRateRange struct {
//	Min float64
//	Max float64
//}
//
//func (b BuyRiseRateRange) Name() string {
//	return "涨幅区间"
//}
//
//func (b BuyRiseRateRange) Buy(code string, dks extend.Klines, mks protocol.Klines) *core.Buy {
//	if len(dks) == 0 {
//		return nil
//	}
//
//	today := dks[len(dks)-1]
//	rate := today.RiseRate()
//	if rate < b.Min {
//		return nil
//	}
//	if b.Max != 0 && rate > b.Max {
//		return nil
//	}
//
//	return &core.Buy{Code: code, Time: today.Time, Price: today.Close}
//}
//
//// BuyBreakHigh 是突破前高买入条件。
//// Period 表示向前统计高点的窗口，默认 20。
//// 要求今天收盘价大于此前 Period 日最高价。
//// 适合表达“收盘突破阶段新高”的强势信号。
//type BuyBreakHigh struct {
//	Period int
//}
//
//func (b BuyBreakHigh) Name() string {
//	return "突破新高"
//}
//
//func (b BuyBreakHigh) Buy(code string, dks extend.Klines, mks protocol.Klines) *core.Buy {
//	if b.Period == 0 {
//		b.Period = 20
//	}
//	if len(dks) < b.Period+1 {
//		return nil
//	}
//
//	today := dks[len(dks)-1]
//	prevHigh := dks[:len(dks)-1].HHV(b.Period)
//	if today.Close <= prevHigh {
//		return nil
//	}
//
//	return &core.Buy{Code: code, Time: today.Time, Price: today.Close}
//}
//
//// BuyNotBreakLow 是不破前低买入条件。
//// Period 表示向前统计低点的窗口，默认 20。
//// 要求今天最低价没有跌破此前 Period 日最低价。
//// 适合作为回调不破位、箱体下沿不破等组合过滤条件。
//type BuyNotBreakLow struct {
//	Period int
//}
//
//func (b BuyNotBreakLow) Name() string {
//	return "不破低点"
//}
//
//func (b BuyNotBreakLow) Buy(code string, dks extend.Klines, mks protocol.Klines) *core.Buy {
//	if b.Period == 0 {
//		b.Period = 20
//	}
//	if len(dks) < b.Period+1 {
//		return nil
//	}
//
//	today := dks[len(dks)-1]
//	prevLow := dks[:len(dks)-1].LLV(b.Period)
//	if today.Low < prevLow {
//		return nil
//	}
//
//	return &core.Buy{Code: code, Time: today.Time, Price: today.Close}
//}
//
//// BuyLongLowerShadow 是长下影线买入条件。
//// Ratio 表示下影线长度占全天振幅的最低比例，默认 0.5。
//// 当下影线占比大于等于 Ratio 时返回买入信号。
//// 适合表达“盘中下探后被资金拉回”的形态。
//type BuyLongLowerShadow struct {
//	Ratio float64
//}
//
//func (b BuyLongLowerShadow) Name() string {
//	return "长下影"
//}
//
//func (b BuyLongLowerShadow) Buy(code string, dks extend.Klines, mks protocol.Klines) *core.Buy {
//	if b.Ratio == 0 {
//		b.Ratio = 0.5
//	}
//	if len(dks) == 0 {
//		return nil
//	}
//
//	today := dks[len(dks)-1]
//	high := today.High.Float64()
//	low := today.Low.Float64()
//	open := today.Open.Float64()
//	close := today.Close.Float64()
//	rangeValue := high - low
//	if rangeValue <= 0 {
//		return nil
//	}
//
//	lower := open
//	if close < lower {
//		lower = close
//	}
//	if (lower-low)/rangeValue < b.Ratio {
//		return nil
//	}
//
//	return &core.Buy{Code: code, Time: today.Time, Price: today.Close}
//}
//
//// BuySmallBody 是小实体 K 线买入条件。
//// Ratio 表示实体长度占全天振幅的最大比例，默认 0.35。
//// 当 K 线实体较小、上下影线相对明显时返回买入信号。
//// 适合识别震荡、整理、犹豫型 K 线，并与趋势或成交量条件组合使用。
//type BuySmallBody struct {
//	Ratio float64
//}
//
//func (b BuySmallBody) Name() string {
//	return "小实体"
//}
//
//func (b BuySmallBody) Buy(code string, dks extend.Klines, mks protocol.Klines) *core.Buy {
//	if b.Ratio == 0 {
//		b.Ratio = 0.35
//	}
//	if len(dks) == 0 {
//		return nil
//	}
//
//	today := dks[len(dks)-1]
//	rangeValue := today.High.Float64() - today.Low.Float64()
//	if rangeValue <= 0 {
//		return nil
//	}
//
//	body := today.Close.Float64() - today.Open.Float64()
//	if body < 0 {
//		body = -body
//	}
//	if body/rangeValue > b.Ratio {
//		return nil
//	}
//
//	return &core.Buy{Code: code, Time: today.Time, Price: today.Close}
//}

// HHV 返回最近 i 根 K 线中的最高价。
// 注意：该函数会对切片进行排序，调用时会改变传入切片尾部窗口的顺序。
// 目前策略中主要用于快速判断近期最高价。
func HHV(dks extend.Klines, i int) protocol.Price {
	ls := dks[len(dks)-i:]
	sort.Slice(ls, func(i, j int) bool { return ls[i].High > ls[j].High })
	return ls[0].High
}

// LLV 返回最近 i 根 K 线中的最低价。
// 注意：该函数会对切片进行排序，调用时会改变传入切片尾部窗口的顺序。
// 目前策略中主要用于快速判断近期最低价。
func LLV(dks extend.Klines, i int) protocol.Price {
	ls := dks[len(dks)-i:]
	sort.Slice(ls, func(i, j int) bool { return ls[i].Low < ls[j].Low })
	return ls[0].Low
}

// A阳线 是阳线买入条件。
// 要求最新交易日收盘价大于开盘价。
// 买入价使用最新交易日收盘价。
// 适合作为 BuyAll 中最基础的 K 线方向过滤条件。
type A阳线 struct{}

func (b A阳线) Name() string {
	return "阳线"
}

func (b A阳线) Buy(code string, dks extend.Klines) bool {
	if len(dks) == 0 {
		return false
	}
	today := dks[len(dks)-1]
	return today.Close > today.Open
}

//// BuyYearUp 是年线向上买入条件。
//// Period 表示年线周期，默认 250。
//// Lookback 表示和多少个交易日前的年线值比较，默认 5。
//// RequireCloseAbove 表示是否要求收盘价在年线上方；当前零值会按 true 处理。
//// 当年线向上，并且收盘价满足位置要求时返回买入信号。
//type BuyYearUp struct {
//	Period            int
//	Lookback          int
//	RequireCloseAbove bool
//}
//
//func (b BuyYearUp) Name() string {
//	return "年线向上"
//}
//
//func (b BuyYearUp) Buy(code string, dks extend.Klines, mks protocol.Klines) *core.Buy {
//	if b.Period == 0 {
//		b.Period = 250
//	}
//	if b.Lookback == 0 {
//		b.Lookback = 5
//	}
//	if !b.RequireCloseAbove {
//		b.RequireCloseAbove = true
//	}
//	if len(dks) < b.Period+b.Lookback {
//		return nil
//	}
//
//	n := len(dks)
//	maNow := core.MA(dks, b.Period)
//	maPrev := core.MA(dks[:n-b.Lookback], b.Period)
//	if maNow <= maPrev {
//		return nil
//	}
//
//	today := dks[n-1]
//	if b.RequireCloseAbove && today.Close.Float64() <= maNow {
//		return nil
//	}
//
//	return &core.Buy{
//		Code:  code,
//		Time:  today.Time,
//		Price: today.Close,
//	}
//}

//// BuyMonthUp 是月线向上买入条件。
//// Period 表示月线周期，默认 20。
//// Lookback 表示和多少个交易日前的月线值比较，默认 5。
//// RequireCloseAbove 表示是否要求收盘价在月线上方；当前零值会按 true 处理。
//// 当月线向上，并且收盘价满足位置要求时返回买入信号。
//type BuyMonthUp struct {
//	Period            int
//	Lookback          int
//	RequireCloseAbove bool
//}
//
//func (b BuyMonthUp) Name() string {
//	return "月线向上"
//}
//
//func (b BuyMonthUp) Buy(code string, dks extend.Klines, mks protocol.Klines) *core.Buy {
//	if b.Period == 0 {
//		b.Period = 20
//	}
//	if b.Lookback == 0 {
//		b.Lookback = 5
//	}
//	if !b.RequireCloseAbove {
//		b.RequireCloseAbove = true
//	}
//	if len(dks) < b.Period+b.Lookback {
//		return nil
//	}
//
//	n := len(dks)
//	maNow := core.MA(dks, b.Period)
//	maPrev := core.MA(dks[:n-b.Lookback], b.Period)
//	if maNow <= maPrev {
//		return nil
//	}
//
//	today := dks[n-1]
//	if b.RequireCloseAbove && today.Close.Float64() <= maNow {
//		return nil
//	}
//
//	return &core.Buy{
//		Code:  code,
//		Time:  today.Time,
//		Price: today.Close,
//	}
//}

//// BuyYearMonthUp 是年线和月线同时向上的买入条件。
//// YearPeriod 表示年线周期，默认 250。
//// MonthPeriod 表示月线周期，默认 20。
//// Lookback 表示和多少个交易日前的均线值比较，默认 5。
//// RequireCloseAbove 表示是否要求收盘价同时站上年线和月线；当前零值会按 true 处理。
//// 适合作为偏中长期趋势方向过滤条件。
//type BuyYearMonthUp struct {
//	YearPeriod        int
//	MonthPeriod       int
//	Lookback          int
//	RequireCloseAbove bool
//}
//
//func (b BuyYearMonthUp) Name() string {
//	return "年线月线向上买入"
//}
//
//func (b BuyYearMonthUp) Buy(code string, dks extend.Klines, mks protocol.Klines) *core.Buy {
//	if b.YearPeriod == 0 {
//		b.YearPeriod = 250
//	}
//	if b.MonthPeriod == 0 {
//		b.MonthPeriod = 20
//	}
//	if b.Lookback == 0 {
//		b.Lookback = 5
//	}
//	if !b.RequireCloseAbove {
//		b.RequireCloseAbove = true
//	}
//	minPeriod := b.YearPeriod
//	if b.MonthPeriod > minPeriod {
//		minPeriod = b.MonthPeriod
//	}
//	if len(dks) < minPeriod+b.Lookback {
//		return nil
//	}
//
//	n := len(dks)
//	yearNow := core.MA(dks, b.YearPeriod)
//	yearPrev := core.MA(dks[:n-b.Lookback], b.YearPeriod)
//	if yearNow <= yearPrev {
//		return nil
//	}
//
//	monthNow := core.MA(dks, b.MonthPeriod)
//	monthPrev := core.MA(dks[:n-b.Lookback], b.MonthPeriod)
//	if monthNow <= monthPrev {
//		return nil
//	}
//
//	today := dks[n-1]
//	if b.RequireCloseAbove && (today.Close.Float64() <= yearNow || today.Close.Float64() <= monthNow) {
//		return nil
//	}
//
//	return &core.Buy{
//		Code:  code,
//		Time:  today.Time,
//		Price: today.Close,
//	}
//}
