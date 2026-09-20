package factor

import (
	"fmt"
	"math"

	"github.com/injoyai/strategy-tail/lib/extend"
)

const (
	// skipMonthTradingDays 固定跳过最近约一个交易月，避免把短期反转混入中期动量。
	skipMonthTradingDays = 21
	// amountScaleYuan 把 Amihud 原始的“每元成交额”缩放成“每 1 亿元成交额”。
	amountScaleYuan = 100_000_000
	// marketCapScaleYuan 让对数市值以亿元为输入，数值更便于阅读；缩放不改变横截面排序。
	marketCapScaleYuan = 100_000_000
)

// 跳过近月动量 是从 N 个交易日前到 21 个交易日前的累计收益率。
// 默认 N=252，近似经典 12-1 月动量；N 必须大于 21。
type 跳过近月动量 struct {
	Days int
}

func (f 跳过近月动量) Name() string {
	return fmt.Sprintf("跳过近月动量(%d)", daysOr(f.Days, 252))
}

func (f 跳过近月动量) Value(code string, dks extend.Klines) float64 {
	days := daysOr(f.Days, 252)
	if days <= skipMonthTradingDays || len(dks) < days+1 {
		return math.NaN()
	}
	start := dks[len(dks)-days-1].Close.Float64()
	end := dks[len(dks)-skipMonthTradingDays-1].Close.Float64()
	if start <= 0 || end <= 0 {
		return math.NaN()
	}
	return end/start - 1
}

// N日短期反转 是近 N 日收益率的相反数。正值表示近期下跌，负值表示近期上涨。
type N日短期反转 struct {
	Days int
}

func (f N日短期反转) Name() string {
	return fmt.Sprintf("N日短期反转(%d)", daysOr(f.Days, 20))
}

func (f N日短期反转) Value(code string, dks extend.Klines) float64 {
	days := daysOr(f.Days, 20)
	if len(dks) < days+1 {
		return math.NaN()
	}
	start := dks[len(dks)-days-1].Close.Float64()
	end := dks[len(dks)-1].Close.Float64()
	if start <= 0 || end <= 0 {
		return math.NaN()
	}
	return -(end/start - 1)
}

// Amihud非流动性 是近 N 日 |日收益率|/成交额 的均值，并按 1 亿元缩放。
// 数值越大，表示相同成交额伴随的价格变动越大、流动性越弱。
type Amihud非流动性 struct {
	Days int
}

func (f Amihud非流动性) Name() string {
	return fmt.Sprintf("Amihud非流动性(%d)", daysOr(f.Days, 20))
}

func (f Amihud非流动性) Value(code string, dks extend.Klines) float64 {
	days := daysOr(f.Days, 20)
	if len(dks) < days+1 {
		return math.NaN()
	}
	window := dks[len(dks)-days-1:]
	var sum float64
	for i := 1; i < len(window); i++ {
		previous := window[i-1].Close.Float64()
		current := window[i].Close.Float64()
		amount := window[i].Amount.Float64()
		if previous <= 0 || current <= 0 || amount <= 0 {
			return math.NaN()
		}
		dailyReturn := current/previous - 1
		sum += math.Abs(dailyReturn) / amount * amountScaleYuan
	}
	return sum / float64(days)
}

// N日收盘高点距离 是当日收盘相对近 N 日最高收盘的距离，值域为 [-1,0]。
type N日收盘高点距离 struct {
	Days int
}

func (f N日收盘高点距离) Name() string {
	return fmt.Sprintf("N日收盘高点距离(%d)", daysOr(f.Days, 252))
}

func (f N日收盘高点距离) Value(code string, dks extend.Klines) float64 {
	days := daysOr(f.Days, 252)
	if len(dks) < days {
		return math.NaN()
	}
	window := dks[len(dks)-days:]
	highest := 0.0
	for _, kline := range window {
		closePrice := kline.Close.Float64()
		if closePrice > highest {
			highest = closePrice
		}
	}
	current := window[len(window)-1].Close.Float64()
	if highest <= 0 || current <= 0 {
		return math.NaN()
	}
	return current/highest - 1
}

// 对数流通市值 是当日流通市值（亿元）的自然对数。
// FloatStock 缺失或收盘价无效时返回 NaN，不以总股本替代。
type 对数流通市值 struct{}

func (对数流通市值) Name() string { return "对数流通市值" }

func (对数流通市值) Value(code string, dks extend.Klines) float64 {
	if len(dks) == 0 {
		return math.NaN()
	}
	current := dks[len(dks)-1]
	closePrice := current.Close.Float64()
	if closePrice <= 0 || current.FloatStock <= 0 {
		return math.NaN()
	}
	marketCapYiYuan := closePrice * float64(current.FloatStock) / marketCapScaleYuan
	if marketCapYiYuan <= 0 || math.IsInf(marketCapYiYuan, 0) {
		return math.NaN()
	}
	return math.Log(marketCapYiYuan)
}
