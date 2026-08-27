package buy

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

// makeKline 构造一根 K 线。
func makeKline(t time.Time, open, close, low, high float64, vol int64) *extend.Kline {
	return &extend.Kline{
		Kline: &protocol.Kline{
			Time:   t,
			Open:   protocol.Price(open * 1000),
			Close:  protocol.Price(close * 1000),
			Low:    protocol.Price(low * 1000),
			High:   protocol.Price(high * 1000),
			Volume: vol,
		},
	}
}

// buildDownthenStabilize 构造 149 根日线：
// 前 140 日缓慢下跌(价格从 100 -> 40, 放量)，之后 8 日企稳横盘微升且缩量，
// 最后 1 日 3 倍倍量阳线。模拟"长期下跌 -> 刚企稳 -> 倍量"的真实形态。
func buildDownthenStabilize() extend.Klines {
	ks := make(extend.Klines, 0, 149)
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	// 前 140 根：缓慢下跌放量
	for i := 0; i < 140; i++ {
		t := base.AddDate(0, 0, i)
		price := 100 - 60*float64(i)/140 // 100 -> 40
		ks = append(ks, makeKline(t, price+1, price, price-2, price+2, 2000000))
	}
	// 8 根：企稳横盘微升缩量
	for i := 0; i < 8; i++ {
		t := base.AddDate(0, 0, 140+i)
		price := 40 + 0.5*float64(i)/8 // 40 -> 40.5
		ks = append(ks, makeKline(t, price, price+0.4, price-0.2, price+0.6, 300000))
	}
	// 最后 1 根：3倍倍量阳线（量略大于昨日3倍，满足严格大于）
	last := ks[len(ks)-1]
	t := last.Time.AddDate(0, 0, 1)
	ks = append(ks, makeKline(t, last.Close.Float64(), last.Close.Float64()+2, last.Low.Float64()-0.5, last.Close.Float64()+3, 300000*3+1))
	return ks
}

func Test下跌企稳倍量_Deep(t *testing.T) {
	ks := buildDownthenStabilize()
	b := A下跌企稳倍量{Mode: "deep"}
	if !b.Buy("sh600000", ks) {
		t.Errorf("deep 模式应命中: 长期下跌40%%+企稳+缩量+3倍倍量")
	}
}

func Test下跌企稳倍量_Mild(t *testing.T) {
	ks := buildDownthenStabilize()
	b := A下跌企稳倍量{Mode: "mild"}
	if !b.Buy("sh600000", ks) {
		t.Errorf("mild 模式应命中: 回撤55%%满足25%%阈值")
	}
}

func Test下跌企稳倍量_None(t *testing.T) {
	ks := buildDownthenStabilize()
	b := A下跌企稳倍量{Mode: "none"}
	if !b.Buy("sh600000", ks) {
		t.Errorf("none 模式应命中: 仅企稳+缩量+倍量")
	}
}

func Test下跌企稳倍量_无倍量(t *testing.T) {
	ks := buildDownthenStabilize()
	// 去掉倍量: 将最后一根量改成与前日相同
	last := ks[len(ks)-1]
	last.Volume = ks[len(ks)-2].Volume
	b := A下跌企稳倍量{Mode: "deep"}
	if b.Buy("sh600000", ks) {
		t.Errorf("无倍量时不应命中")
	}
}

func Test下跌企稳倍量_无企稳(t *testing.T) {
	ks := buildDownthenStabilize()
	// 破坏企稳: 将倍量日之前 8 根横盘全部改为持续阴线创新低
	for i := 140; i < len(ks)-1; i++ {
		k := ks[i]
		k.Open = protocol.Price(42 * 1000)
		k.Close = protocol.Price(float64(42 - (i-139)*2) * 1000) // 40 -> 24 阴跌
		k.Low = protocol.Price((float64(42-(i-139)*2) - 2) * 1000)
		k.High = protocol.Price(43 * 1000)
	}
	b := A下跌企稳倍量{Mode: "deep"}
	if b.Buy("sh600000", ks) {
		t.Errorf("无企稳形态时不应命中")
	}
}

func Test下跌企稳倍量_未深跌(t *testing.T) {
	ks := buildDownthenStabilize()
	// 把下跌幅度改浅: 前 140 日从 100 跌到 85 (回撤 15%)，横盘段与倍量日也相应从 85 起
	for i := 0; i < 140; i++ {
		price := 100 - 15*float64(i)/140
		k := ks[i]
		k.Open = protocol.Price((price + 1) * 1000)
		k.Close = protocol.Price(price * 1000)
		k.Low = protocol.Price((price - 2) * 1000)
		k.High = protocol.Price((price + 2) * 1000)
	}
	for i := 140; i < len(ks)-1; i++ {
		k := ks[i]
		price := 85 + 0.5*float64(i-140)/8
		k.Open = protocol.Price(price * 1000)
		k.Close = protocol.Price((price + 0.4) * 1000)
		k.Low = protocol.Price((price - 0.2) * 1000)
		k.High = protocol.Price((price + 0.6) * 1000)
	}
	last := ks[len(ks)-1]
	last.Open = protocol.Price(85.5 * 1000)
	last.Close = protocol.Price(87.5 * 1000)
	last.Low = protocol.Price(85 * 1000)
	last.High = protocol.Price(88 * 1000)
	b := A下跌企稳倍量{Mode: "deep"}
	if b.Buy("sh600000", ks) {
		t.Errorf("deep 模式回撤不足40%%不应命中")
	}
	bMild := A下跌企稳倍量{Mode: "mild"}
	if bMild.Buy("sh600000", ks) {
		t.Errorf("mild 模式回撤不足25%%也不应命中")
	}
}

// buildStabilizeNoDowntrend 构造"非下跌背景 + 企稳缩量 + 倍量"的形态：
// 前 120 日横盘震荡于 60 附近（无长期下跌），随后 8 日缩量企稳微升，最后 1 日 3 倍倍量阳线。
// 用于验证 confirm5 档（去掉下跌过滤、加站上5日线）的命中与互斥条件。
func buildStabilizeNoDowntrend() extend.Klines {
	ks := make(extend.Klines, 0, 135)
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	// 前 126 根：横盘震荡（价格 58~62 波动），正常量
	for i := 0; i < 126; i++ {
		t := base.AddDate(0, 0, i)
		c := 60 + 2*sinf(float64(i)*0.7) // 58~62 波动
		ks = append(ks, makeKline(t, c+0.3, c, c-0.5, c+0.6, 1500000))
	}
	// 8 根：企稳横盘微升缩量（60 -> 60.5）
	for i := 0; i < 8; i++ {
		t := base.AddDate(0, 0, 126+i)
		price := 60 + 0.5*float64(i)/8
		ks = append(ks, makeKline(t, price, price+0.4, price-0.2, price+0.6, 250000))
	}
	// 最后 1 根：3倍倍量阳线（量略大于昨日3倍，满足严格大于）
	last := ks[len(ks)-1]
	t := last.Time.AddDate(0, 0, 1)
	ks = append(ks, makeKline(t, last.Close.Float64(), last.Close.Float64()+2, last.Low.Float64()-0.5, last.Close.Float64()+3, 250000*3+1))
	return ks
}

// sinf 简易正弦，避免引入 math 之外的依赖。
func sinf(x float64) float64 {
	// 用分段近似生成 58~62 的波动即可，无需精确正弦
	if int(x)%3 == 0 {
		return 1
	}
	if int(x)%3 == 1 {
		return -1
	}
	return 0
}

func Test企稳倍量_Confirm5_命中(t *testing.T) {
	ks := buildStabilizeNoDowntrend()
	b := A下跌企稳倍量{Mode: "confirm5"}
	if !b.Buy("sh600000", ks) {
		t.Errorf("confirm5 档应命中: 去掉下跌过滤+企稳+缩量+倍量+收盘站上5日线")
	}
	// 同时验证 deep 档在非深跌背景下不命中（回撤不足）
	bDeep := A下跌企稳倍量{Mode: "deep"}
	if bDeep.Buy("sh600000", ks) {
		t.Errorf("deep 档在非深跌背景下不应命中")
	}
}

func Test企稳倍量_Confirm5_未站上5日线(t *testing.T) {
	ks := buildStabilizeNoDowntrend()
	// 将倍量日收盘压低，使其 <= 截止昨日的 5 日均线，但仍满足倍量
	last := ks[len(ks)-1]
	ma5 := coreMA5(ks[:len(ks)-1])
	last.Close = protocol.Price((ma5 - 0.5) * 1000)
	last.High = protocol.Price((ma5 + 0.5) * 1000)
	last.Open = protocol.Price((ma5 - 1.5) * 1000)
	b := A下跌企稳倍量{Mode: "confirm5"}
	if b.Buy("sh600000", ks) {
		t.Errorf("倍量日收盘未站上5日线时 confirm5 不应命中")
	}
	// 但 none 档（无站上5日线要求）仍应命中
	bNone := A下跌企稳倍量{Mode: "none"}
	if !bNone.Buy("sh600000", ks) {
		t.Errorf("none 档不要求站上5日线，应命中")
	}
}

// coreMA5 计算切片最后一根截止的 5 日均线（收盘均值），供测试断言。
func coreMA5(ks extend.Klines) float64 {
	if len(ks) < 5 {
		return 0
	}
	tail := ks[len(ks)-5:]
	sum := 0.0
	for _, k := range tail {
		sum += k.Close.Float64()
	}
	return sum / 5
}

// buildTdxVolume 构造"非下跌背景 + 企稳缩量 + 通达信倍量"形态：
// 前 126 日横盘震荡，随后 8 日缩量企稳微升，最后 1 日量能为昨日的 2.2 倍（满足通达信>2倍，
// 但 2.2 倍 < 3 倍，不满足默认双重 3 倍条件）。用于验证 TdxVolume 开关的差异。
func buildTdxVolume() extend.Klines {
	ks := make(extend.Klines, 0, 135)
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	for i := 0; i < 126; i++ {
		t := base.AddDate(0, 0, i)
		c := 60 + 2*sinf(float64(i)*0.7)
		ks = append(ks, makeKline(t, c+0.3, c, c-0.5, c+0.6, 1500000))
	}
	for i := 0; i < 8; i++ {
		t := base.AddDate(0, 0, 126+i)
		price := 60 + 0.5*float64(i)/8
		ks = append(ks, makeKline(t, price, price+0.4, price-0.2, price+0.6, 250000))
	}
	last := ks[len(ks)-1]
	t := last.Time.AddDate(0, 0, 1)
	// 倍量日量能为昨日 2.2 倍，收盘站上5日线
	ks = append(ks, makeKline(t, last.Close.Float64(), last.Close.Float64()+2, last.Low.Float64()-0.5, last.Close.Float64()+3, 250000*22/10))
	return ks
}

func Test企稳倍量_Confirm5_通达信倍量(t *testing.T) {
	ks := buildTdxVolume()
	b := A下跌企稳倍量{Mode: "confirm5", TdxVolume: true}
	if !b.Buy("sh600000", ks) {
		t.Errorf("confirm5+通达信倍量 应命中: 今日量>昨日量x2 (2.2倍) + 站上5日线")
	}
	// 默认双重 3 倍条件不应命中（2.2 倍不足 3 倍）
	bDefault := A下跌企稳倍量{Mode: "confirm5"}
	if bDefault.Buy("sh600000", ks) {
		t.Errorf("默认双重3倍条件不应命中 2.2 倍量")
	}
}

func Test企稳倍量_Confirm5_通达信倍量_不足2倍(t *testing.T) {
	ks := buildTdxVolume()
	// 将倍量日量能改为昨日的 1.8 倍，不再满足通达信 >2 倍
	last := ks[len(ks)-1]
	last.Volume = ks[len(ks)-2].Volume * 18 / 10
	b := A下跌企稳倍量{Mode: "confirm5", TdxVolume: true}
	if b.Buy("sh600000", ks) {
		t.Errorf("通达信倍量 1.8 倍不足 2 倍，不应命中")
	}
}
