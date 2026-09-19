package factor

import (
	"fmt"
	"math"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/util"
)

const (
	macdFast            = 12
	macdSlow            = 26
	macdSignal          = 9
	macdMinSamples      = macdSlow + macdSignal
	maSlopeLookback     = 5
	defaultMACDLookback = 4
)

// MACD柱强度 是当日 MACD 柱值相对收盘价的比例：hist / close。
// 固定使用与 common.MACDBuyer 相同的 12/26/9 参数；负值表示量柱位于零轴下方。
type MACD柱强度 struct{}

func (f MACD柱强度) Name() string { return "MACD柱强度" }

func (f MACD柱强度) Value(code string, dks extend.Klines) float64 {
	hist, ok := standardMACD(dks)
	if !ok {
		return math.NaN()
	}
	close := dks[len(dks)-1].Close.Float64()
	if close == 0 {
		return math.NaN()
	}
	return hist[len(hist)-1] / close
}

// MACD柱增量 是当日 MACD 柱相对昨日的增量，再除以当日收盘价：
// (hist[t] - hist[t-1]) / close[t]。正值对应 MACD反转 的“今天柱子变大”。
type MACD柱增量 struct{}

func (f MACD柱增量) Name() string { return "MACD柱增量" }

func (f MACD柱增量) Value(code string, dks extend.Klines) float64 {
	hist, ok := standardMACD(dks)
	if !ok {
		return math.NaN()
	}
	n := len(dks)
	close := dks[n-1].Close.Float64()
	if close == 0 {
		return math.NaN()
	}
	return (hist[n-1] - hist[n-2]) / close
}

// MACD低位位置 是昨日 MACD 柱在截至昨日的近 N 日区间中的位置：
// (hist[t-1] - min) / (max - min)。0 表示昨日为窗口最低点，1 表示最高点。
// 窗口内柱值全部相等时返回 0，与 MACD反转 将昨日视为最低点的语义一致。
type MACD低位位置 struct {
	Days int
}

func (f MACD低位位置) Name() string {
	return fmt.Sprintf("MACD低位位置(%d)", daysOr(f.Days, defaultMACDLookback))
}

func (f MACD低位位置) Value(code string, dks extend.Klines) float64 {
	days := daysOr(f.Days, defaultMACDLookback)
	hist, ok := standardMACD(dks)
	if !ok || len(hist) < days+1 {
		return math.NaN()
	}

	n := len(hist)
	window := hist[n-1-days : n-1]
	lo, hi := window[0], window[0]
	for _, value := range window[1:] {
		lo = math.Min(lo, value)
		hi = math.Max(hi, value)
	}
	if hi == lo {
		return 0
	}
	return (hist[n-2] - lo) / (hi - lo)
}

// MACD负柱连续天数 是从当日向前连续小于零的 MACD 柱数量。
// 原始值 >= 5 对应 common.MACDBuyer 中 MACD负数{MinDays: 5} 的主要条件。
type MACD负柱连续天数 struct{}

func (f MACD负柱连续天数) Name() string { return "MACD负柱连续天数" }

func (f MACD负柱连续天数) Value(code string, dks extend.Klines) float64 {
	hist, ok := standardMACD(dks)
	if !ok {
		return math.NaN()
	}
	count := 0
	for i := len(hist) - 1; i >= 0 && hist[i] < 0; i-- {
		count++
	}
	return float64(count)
}

// MACD柱连续增长天数 是从当日向前连续满足 hist[i] > hist[i-1] 的上涨步数。
// 今天首次高于昨天时返回 1；今天未高于昨天时返回 0。该口径与
// buy.MACD连涨 的 streakDays 完全一致。
type MACD柱连续增长天数 struct{}

func (f MACD柱连续增长天数) Name() string { return "MACD柱连续增长天数" }

func (f MACD柱连续增长天数) Value(code string, dks extend.Klines) float64 {
	hist, ok := standardMACD(dks)
	if !ok {
		return math.NaN()
	}
	count := 0
	for i := len(hist) - 1; i > 0 && hist[i] > hist[i-1]; i-- {
		count++
	}
	return float64(count)
}

// 均线最弱日斜率 是最近 5 个交易日中，N 日均线逐日相对涨速的最小值。
// 正值表示 5 步全部向上；阈值 0.0002/0.0005 分别对应 common.MACDBuyer
// 中 20/30 日 MAUp 的 MinSlope 条件。
type 均线最弱日斜率 struct {
	Days int
}

func (f 均线最弱日斜率) Name() string {
	return fmt.Sprintf("均线最弱日斜率(%d)", daysOr(f.Days, 20))
}

func (f 均线最弱日斜率) Value(code string, dks extend.Klines) float64 {
	period := daysOr(f.Days, 20)
	if len(dks) < period+maSlopeLookback {
		return math.NaN()
	}

	n := len(dks)
	weakest := math.Inf(1)
	for offset := 0; offset < maSlopeLookback; offset++ {
		maNow := dks[:n-offset].MA(period).Float64()
		maPrev := dks[:n-offset-1].MA(period).Float64()
		if maPrev <= 0 {
			return math.NaN()
		}
		slope := (maNow - maPrev) / maPrev
		weakest = math.Min(weakest, slope)
	}
	return weakest
}

func standardMACD(dks extend.Klines) ([]float64, bool) {
	if len(dks) < macdMinSamples {
		return nil, false
	}
	hist := util.MACDHistogram(dks, macdFast, macdSlow, macdSignal)
	return hist, len(hist) == len(dks)
}
