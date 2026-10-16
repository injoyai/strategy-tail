package factor

import (
	"fmt"
	"math"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/util"
	"github.com/injoyai/tdx/protocol"
)

// 这些底层因子供现有条件组合使用，不自动加入研究目录。
// 因子只计算数值；条件保留预热期、默认阈值与严格/包含边界的判断。

// 均线 返回近 N 日均线，保留行情价格精度；0 默认 20 日。
type A均线 int

func (f A均线) Name() string { return fmt.Sprintf("均线(%d)", daysOr(int(f), 20)) }
func (f A均线) Value(code string, dks extend.Klines) float64 {
	if len(dks) < daysOr(int(f), 20) {
		return math.NaN()
	}
	return f.Price(dks).Float64()
}

// Price 供条件保留整数价格比较；缺数据返回 0，调用方须先校验窗口。
func (f A均线) Price(dks extend.Klines) protocol.Price {
	n := daysOr(int(f), 20)
	if len(dks) < n {
		return 0
	}
	return dks.MA(n)
}

// 前N日均量 不含今日，使用浮点累计保留原买卖条件的运算顺序；0 默认 5 日。
type A前N日均量 int

func (f A前N日均量) Name() string { return fmt.Sprintf("前N日均量(%d)", daysOr(int(f), 5)) }
func (f A前N日均量) Value(code string, dks extend.Klines) float64 {
	n := daysOr(int(f), 5)
	if len(dks) < n+1 {
		return math.NaN()
	}
	return A均量(n).Value(code, dks[:len(dks)-1])
}

// RSI值 使用条件共用的浮点 RSI，0 默认 14 日。
type RSI值 int

func (f RSI值) Name() string { return fmt.Sprintf("RSI(%d)", daysOr(int(f), 14)) }
func (f RSI值) Value(code string, dks extend.Klines) float64 {
	n := daysOr(int(f), 14)
	if len(dks) < n+1 {
		return math.NaN()
	}
	return util.CalcRSI(dks, n)
}

// 整数RSI值 保留历史“RSI恢复”条件的行情整数算法，与浮点 RSI 不混用。
type A整数RSI值 int

func (f A整数RSI值) Name() string { return fmt.Sprintf("整数RSI(%d)", daysOr(int(f), 14)) }
func (f A整数RSI值) Value(code string, dks extend.Klines) float64 {
	n := daysOr(int(f), 14)
	if len(dks) < n+1 {
		return math.NaN()
	}
	return float64(dks.RSI(n))
}

// ATR值 返回近 N 个真实波幅的简单均值（元），0 默认 14 日。
type ATR值 int

func (f ATR值) Name() string { return fmt.Sprintf("ATR(%d)", daysOr(int(f), 14)) }
func (f ATR值) Value(code string, dks extend.Klines) float64 {
	n := daysOr(int(f), 14)
	if len(dks) < n+1 {
		return math.NaN()
	}
	var sum float64
	for i := len(dks) - n; i < len(dks); i++ {
		high, low, prev := dks[i].High.Float64(), dks[i].Low.Float64(), dks[i-1].Close.Float64()
		tr := high - low
		if d := high - prev; d > tr {
			tr = d
		}
		if d := prev - low; d > tr {
			tr = d
		}
		sum += tr
	}
	return sum / float64(n)
}

// closeMeanStd 使用浮点收盘均值与总体标准差，保留布林/ZScore 的历史精度。
// 数据不足返回 NaN；不能替换成会按价格单位截断的行情均线。
func closeMeanStd(dks extend.Klines, n int) (float64, float64) {
	if n <= 0 || len(dks) < n {
		return math.NaN(), math.NaN()
	}
	window := dks[len(dks)-n:]
	var sum float64
	for _, k := range window {
		sum += k.Close.Float64()
	}
	mean := sum / float64(n)
	var sq float64
	for _, k := range window {
		d := k.Close.Float64() - mean
		sq += d * d
	}
	return mean, math.Sqrt(sq / float64(n))
}

// 布林下轨 的 Days 默认 20，StdTimes 默认 2；零方差返回 NaN。
type A布林下轨 struct {
	Days     int
	StdTimes float64
}

func (f A布林下轨) multiple() float64 {
	multiple := f.StdTimes
	if multiple == 0 {
		multiple = 2
	}
	return multiple
}
func (f A布林下轨) Name() string {
	return fmt.Sprintf("布林下轨(%d,%gσ)", daysOr(f.Days, 20), f.multiple())
}
func (f A布林下轨) Value(code string, dks extend.Klines) float64 {
	mean, std := closeMeanStd(dks, daysOr(f.Days, 20))
	if std <= 0 {
		return math.NaN()
	}
	return mean - f.multiple()*std
}

// ZScore值 是浮点收盘偏离均值的标准差倍数；0 默认 20 日，零方差返回 NaN。
type ZScore值 int

func (f ZScore值) Name() string { return fmt.Sprintf("ZScore(%d)", daysOr(int(f), 20)) }
func (f ZScore值) Value(code string, dks extend.Klines) float64 {
	mean, std := closeMeanStd(dks, daysOr(int(f), 20))
	if len(dks) == 0 || std <= 0 {
		return math.NaN()
	}
	return (dks[len(dks)-1].Close.Float64() - mean) / std
}

// MACD柱值 保留原始柱值（不除收盘价、不乘 2），参数 0 默认 12/26/9。
// Series 允许时序条件一次计算整段柱值；预热期由原条件保持各自口径。
type MACD柱值 struct {
	Fast   int
	Slow   int
	Signal int
}

func (f MACD柱值) params() (int, int, int) {
	fast, slow, signal := f.Fast, f.Slow, f.Signal
	if fast == 0 {
		fast = 12
	}
	if slow == 0 {
		slow = 26
	}
	if signal == 0 {
		signal = 9
	}
	return fast, slow, signal
}
func (f MACD柱值) Name() string {
	fast, slow, signal := f.params()
	return fmt.Sprintf("MACD柱值(%d,%d,%d)", fast, slow, signal)
}
func (f MACD柱值) Series(dks extend.Klines) []float64 {
	fast, slow, signal := f.params()
	return util.MACDHistogram(dks, fast, slow, signal)
}
func (f MACD柱值) Value(code string, dks extend.Klines) float64 {
	hist := f.Series(dks)
	if len(hist) == 0 {
		return math.NaN()
	}
	return hist[len(hist)-1]
}

// MACD平滑柱值 对原始 MACD 柱再次 EMA 平滑，参数 0 默认 12/26/9/5。
// Series 供时序条件一次读取整段序列，预热期仍由各条件校验。
type MACD平滑柱值 struct {
	Fast   int
	Slow   int
	Signal int
	Smooth int
}

func (f MACD平滑柱值) smoothPeriod() int {
	if f.Smooth == 0 {
		return 5
	}
	return f.Smooth
}
func (f MACD平滑柱值) Name() string {
	fast, slow, signal := (MACD柱值{Fast: f.Fast, Slow: f.Slow, Signal: f.Signal}).params()
	return fmt.Sprintf("MACD平滑柱值(%d,%d,%d,%d)", fast, slow, signal, f.smoothPeriod())
}
func (f MACD平滑柱值) Series(dks extend.Klines) []float64 {
	fast, slow, signal := (MACD柱值{Fast: f.Fast, Slow: f.Slow, Signal: f.Signal}).params()
	return util.SmoothedMACDHistogram(dks, fast, slow, signal, f.smoothPeriod())
}
func (f MACD平滑柱值) Value(code string, dks extend.Klines) float64 {
	hist := f.Series(dks)
	if len(hist) == 0 {
		return math.NaN()
	}
	return hist[len(hist)-1]
}

// MACDDIF值 是快慢 EMA 的原始差值，参数 0 默认 12/26。
type MACDDIF值 struct {
	Fast int
	Slow int
}

func (f MACDDIF值) Name() string {
	fast, slow, _ := (MACD柱值{Fast: f.Fast, Slow: f.Slow}).params()
	return fmt.Sprintf("MACDDIF(%d,%d)", fast, slow)
}
func (f MACDDIF值) Value(code string, dks extend.Klines) float64 {
	dif := f.Series(dks)
	if len(dif) == 0 {
		return math.NaN()
	}
	return dif[len(dif)-1]
}

func (f MACDDIF值) Series(dks extend.Klines) []float64 {
	fast, slow, _ := (MACD柱值{Fast: f.Fast, Slow: f.Slow}).params()
	return util.MACDDIF(dks, fast, slow)
}
