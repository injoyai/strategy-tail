package buy

import (
	"fmt"
	"math"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/util"
)

// ----------------------------------------------------------------------------
// 公共计算工具（浮点精度，避免 BOLL 整数 std 的精度损失）
// ----------------------------------------------------------------------------

// meanStd 计算近 n 日收盘价的均值与样本标准差。
// 返回 (ma, std)；数据不足时 std=0。
func meanStd(dks extend.Klines, n int) (float64, float64) {
	if len(dks) < n || n <= 0 {
		return 0, 0
	}
	window := dks[len(dks)-n:]
	sum := 0.0
	for _, k := range window {
		sum += k.Close.Float64()
	}
	ma := sum / float64(n)
	var sq float64
	for _, k := range window {
		d := k.Close.Float64() - ma
		sq += d * d
	}
	std := math.Sqrt(sq / float64(n))
	return ma, std
}

// ----------------------------------------------------------------------------
// 1. A布林下轨 — 收盘价跌破布林下轨买入（最经典的均值回归入场）
// ----------------------------------------------------------------------------

// A布林下轨 是布林带下轨回归买入策略。
// Period 表示布林带计算周期，默认 20。
// StdTimes 表示标准差倍数，默认 2。
// 触发条件：最新收盘价 <= 布林下轨（mid - StdTimes*std）。
// 逻辑：价格偏离均值超过 StdTimes 个标准差，预期回归中轨。
type A布林下轨 struct {
	Period   int
	StdTimes float64
}

func (s A布林下轨) Name() string {
	p := s.Period
	if p == 0 {
		p = 20
	}
	st := s.StdTimes
	if st == 0 {
		st = 2
	}
	return fmt.Sprintf("布林%d下轨%.0fσ", p, st)
}

func (s A布林下轨) Buy(code string, dks extend.Klines) bool {
	p := s.Period
	if p == 0 {
		p = 20
	}
	st := s.StdTimes
	if st == 0 {
		st = 2
	}
	if len(dks) < p {
		return false
	}
	ma, std := meanStd(dks, p)
	if std <= 0 {
		return false
	}
	lower := ma - st*std
	return dks[len(dks)-1].Close.Float64() <= lower
}

// ----------------------------------------------------------------------------
// 2. A乖离超卖 — 乖离率 BIAS 偏离过大买入
// ----------------------------------------------------------------------------

// A乖离超卖 是乖离率回归买入策略。
// Period 表示均线周期，默认 20。
// MinBias 表示最低乖离率（%），默认 -7，即 (Close-MA)/MA*100 <= -7 时买入。
// 逻辑：价格相对均线负偏离超过阈值，预期向均线回归。
type A乖离超卖 struct {
	Period  int
	MinBias float64
}

func (s A乖离超卖) Name() string {
	p := s.Period
	if p == 0 {
		p = 20
	}
	mb := s.MinBias
	if mb == 0 {
		mb = -7
	}
	return fmt.Sprintf("乖离%d日<%.1f%%", p, mb)
}

func (s A乖离超卖) Buy(code string, dks extend.Klines) bool {
	p := s.Period
	if p == 0 {
		p = 20
	}
	mb := s.MinBias
	if mb == 0 {
		mb = -7
	}
	if len(dks) < p {
		return false
	}
	ma := dks.MA(p).Float64()
	if ma <= 0 {
		return false
	}
	bias := (dks[len(dks)-1].Close.Float64() - ma) / ma * 100
	return bias <= mb
}

// ----------------------------------------------------------------------------
// 3. AZScore超卖 — 标准化偏离买入
// ----------------------------------------------------------------------------

// AZScore超卖 是 Z-Score 偏离买入策略。
// Period 表示统计窗口，默认 20。
// MinZ 表示最低 Z 值，默认 -2，即 Z <= -2 时买入。
// Z = (Close - MA) / Std，衡量价格偏离均值的标准化程度。
// 逻辑：Z < -2 表示价格偏离均值超过 2 倍标准差，预期回归。
type AZScore超卖 struct {
	Period int
	MinZ   float64
}

func (s AZScore超卖) Name() string {
	p := s.Period
	if p == 0 {
		p = 20
	}
	mz := s.MinZ
	if mz == 0 {
		mz = -2
	}
	return fmt.Sprintf("ZScore%d<%.1f", p, mz)
}

func (s AZScore超卖) Buy(code string, dks extend.Klines) bool {
	p := s.Period
	if p == 0 {
		p = 20
	}
	mz := s.MinZ
	if mz == 0 {
		mz = -2
	}
	if len(dks) < p {
		return false
	}
	ma, std := meanStd(dks, p)
	if std <= 0 {
		return false
	}
	z := (dks[len(dks)-1].Close.Float64() - ma) / std
	return z <= mz
}

// ----------------------------------------------------------------------------
// 4. A唐奇安下沿 — 触及通道下沿反转买入
// ----------------------------------------------------------------------------

// A唐奇安下沿 是唐奇安通道下沿回归买入策略。
// Period 表示通道周期，默认 20。
// 触发条件：最新收盘价 <= 近 Period 日最低价（触及通道下沿）。
// 逻辑：价格跌至近期区间底部，均值回归预期反弹至通道中轨。
type A唐奇安下沿 struct {
	Period int
}

func (s A唐奇安下沿) Name() string {
	p := s.Period
	if p == 0 {
		p = 20
	}
	return fmt.Sprintf("唐奇安%d日下沿", p)
}

func (s A唐奇安下沿) Buy(code string, dks extend.Klines) bool {
	p := s.Period
	if p == 0 {
		p = 20
	}
	if len(dks) < p+1 {
		return false
	}
	// 近 p 日最低（不含今天，避免恒真）
	low := dks[len(dks)-1-p : len(dks)-1].LLV(p)
	return dks[len(dks)-1].Close <= low
}

// ----------------------------------------------------------------------------
// 5. A布林下轨缩量 — 布林下轨 + 地量确认（双重过滤，降低假信号）
// ----------------------------------------------------------------------------

// A布林下轨缩量 是布林带下轨配合缩量确认的买入策略。
// Period 表示布林带周期，默认 20。
// StdTimes 标准差倍数，默认 2。
// VolDays 成交量均量天数，默认 5。
// VolRatio 今日量 <= VolDays 均量的比例，默认 0.6（地量）。
// 逻辑：下轨偏离 + 地量说明卖盘枯竭，回归概率更高。
type A布林下轨缩量 struct {
	Period   int
	StdTimes float64
	VolDays  int
	VolRatio float64
}

func (s A布林下轨缩量) Name() string {
	p := s.Period
	if p == 0 {
		p = 20
	}
	vr := s.VolRatio
	if vr == 0 {
		vr = 0.6
	}
	return fmt.Sprintf("布林%d下轨+地量%.0f%%", p, vr*100)
}

func (s A布林下轨缩量) Buy(code string, dks extend.Klines) bool {
	p := s.Period
	if p == 0 {
		p = 20
	}
	st := s.StdTimes
	if st == 0 {
		st = 2
	}
	vd := s.VolDays
	if vd == 0 {
		vd = 5
	}
	vr := s.VolRatio
	if vr == 0 {
		vr = 0.6
	}
	if len(dks) < p || len(dks) < vd+1 {
		return false
	}
	// 布林下轨
	ma, std := meanStd(dks, p)
	if std <= 0 {
		return false
	}
	lower := ma - st*std
	if dks[len(dks)-1].Close.Float64() > lower {
		return false
	}
	// 地量确认
	today := dks[len(dks)-1]
	var volSum float64
	for i := len(dks) - 1 - vd; i < len(dks)-1; i++ {
		volSum += float64(dks[i].Volume)
	}
	avgVol := volSum / float64(vd)
	if avgVol <= 0 {
		return false
	}
	return float64(today.Volume) <= avgVol*vr
}

// ----------------------------------------------------------------------------
// 6. A布林下轨RSI超卖 — 布林下轨 + RSI 超卖（双重确认）
// ----------------------------------------------------------------------------

// A布林下轨RSI超卖 是布林带下轨与 RSI 超卖的双重确认买入策略。
// Period 布林带周期，默认 20。
// StdTimes 标准差倍数，默认 2。
// RSIPeriod RSI 周期，默认 14。
// RSIThreshold RSI 买入阈值，默认 30。
// 逻辑：价格在下轨且 RSI 超卖，两个独立信号共振，假突破概率更低。
type A布林下轨RSI超卖 struct {
	Period       int
	StdTimes     float64
	RSIPeriod    int
	RSIThreshold float64
}

func (s A布林下轨RSI超卖) Name() string {
	p := s.Period
	if p == 0 {
		p = 20
	}
	rt := s.RSIThreshold
	if rt == 0 {
		rt = 30
	}
	return fmt.Sprintf("布林%d下轨+RSI<%.0f", p, rt)
}

func (s A布林下轨RSI超卖) Buy(code string, dks extend.Klines) bool {
	p := s.Period
	if p == 0 {
		p = 20
	}
	st := s.StdTimes
	if st == 0 {
		st = 2
	}
	rp := s.RSIPeriod
	if rp == 0 {
		rp = 14
	}
	rt := s.RSIThreshold
	if rt == 0 {
		rt = 30
	}
	if len(dks) < p || len(dks) < rp+1 {
		return false
	}
	// 布林下轨
	ma, std := meanStd(dks, p)
	if std <= 0 {
		return false
	}
	lower := ma - st*std
	if dks[len(dks)-1].Close.Float64() > lower {
		return false
	}
	// RSI 超卖
	rsi := util.CalcRSI(dks, rp)
	return rsi <= rt
}
