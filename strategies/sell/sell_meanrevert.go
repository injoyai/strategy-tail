package sell

import (
	"fmt"
	"math"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// ----------------------------------------------------------------------------
// 均值回归卖出策略族
// 接口：Sell(code string, dks extend.Klines, buy core.Buy) bool
// dks 包含到"今天"为止的全部 K 线（含买入日之前的历史数据）。
// 回测引擎保证：买入日当天不会调用 Sell，故 Sell 判断 dks 最后一天即可。
// ----------------------------------------------------------------------------

// meanStd 计算近 n 日收盘价均值与样本标准差（浮点精度）。
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
	return ma, math.Sqrt(sq / float64(n))
}

// ----------------------------------------------------------------------------
// 1. A回到布林中轨 — 价格回归布林中轨卖出
// ----------------------------------------------------------------------------

// A回到布林中轨 是价格回归布林带中轨的卖出策略。
// Period 布林带周期，默认 20。
// 触发条件：最新收盘价 >= 布林中轨（MA）。
// 逻辑：均值回归目标达成，价格回到均值后获利了结。
type A回到布林中轨 struct {
	Period int
}

func (s A回到布林中轨) Name() string {
	p := s.Period
	if p == 0 {
		p = 20
	}
	return fmt.Sprintf("回归布林%d中轨", p)
}

func (s A回到布林中轨) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	p := s.Period
	if p == 0 {
		p = 20
	}
	if len(dks) < p {
		return false
	}
	ma := dks.MA(p).Float64()
	if ma <= 0 {
		return false
	}
	return dks[len(dks)-1].Close.Float64() >= ma
}

// ----------------------------------------------------------------------------
// 2. A回到均线 — 价格回归指定均线卖出
// ----------------------------------------------------------------------------

// A回到均线 是价格回归均线的卖出策略。
// Period 均线周期，默认 20。
// 触发条件：最新收盘价 >= MA(Period)。
type A回到均线 struct {
	Period int
}

func (s A回到均线) Name() string {
	p := s.Period
	if p == 0 {
		p = 20
	}
	return fmt.Sprintf("回归MA%d", p)
}

func (s A回到均线) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	p := s.Period
	if p == 0 {
		p = 20
	}
	if len(dks) < p {
		return false
	}
	ma := dks.MA(p).Float64()
	if ma <= 0 {
		return false
	}
	return dks[len(dks)-1].Close.Float64() >= ma
}

// ----------------------------------------------------------------------------
// 3. A乖离归零 — 乖离率回归 0 卖出
// ----------------------------------------------------------------------------

// A乖离归零 是乖离率回归零轴的卖出策略。
// Period 均线周期，默认 20。
// 触发条件：BIAS = (Close-MA)/MA*100 >= 0。
// 逻辑：负乖离修复到 0 即完成回归。
type A乖离归零 struct {
	Period int
}

func (s A乖离归零) Name() string {
	p := s.Period
	if p == 0 {
		p = 20
	}
	return fmt.Sprintf("乖离%d归零", p)
}

func (s A乖离归零) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	p := s.Period
	if p == 0 {
		p = 20
	}
	if len(dks) < p {
		return false
	}
	ma := dks.MA(p).Float64()
	if ma <= 0 {
		return false
	}
	bias := (dks[len(dks)-1].Close.Float64() - ma) / ma * 100
	return bias >= 0
}

// ----------------------------------------------------------------------------
// 4. AZScore归零 — Z-Score 回归 0 卖出
// ----------------------------------------------------------------------------

// AZScore归零 是 Z-Score 回归零轴的卖出策略。
// Period 统计窗口，默认 20。
// 触发条件：Z = (Close-MA)/Std >= 0。
type AZScore归零 struct {
	Period int
}

func (s AZScore归零) Name() string {
	p := s.Period
	if p == 0 {
		p = 20
	}
	return fmt.Sprintf("ZScore%d归零", p)
}

func (s AZScore归零) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	p := s.Period
	if p == 0 {
		p = 20
	}
	if len(dks) < p {
		return false
	}
	ma, std := meanStd(dks, p)
	if std <= 0 {
		return false
	}
	z := (dks[len(dks)-1].Close.Float64() - ma) / std
	return z >= 0
}

// ----------------------------------------------------------------------------
// 5. A回到唐奇安中轨 — 价格回归通道中轨卖出
// ----------------------------------------------------------------------------

// A回到唐奇安中轨 是价格回归唐奇安通道中轨的卖出策略。
// Period 通道周期，默认 20。
// 触发条件：Close >= (HHV(Period) + LLV(Period)) / 2。
// 逻辑：从下沿反弹至通道中位，回归目标达成。
type A回到唐奇安中轨 struct {
	Period int
}

func (s A回到唐奇安中轨) Name() string {
	p := s.Period
	if p == 0 {
		p = 20
	}
	return fmt.Sprintf("回归唐奇安%d中轨", p)
}

func (s A回到唐奇安中轨) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	p := s.Period
	if p == 0 {
		p = 20
	}
	if len(dks) < p {
		return false
	}
	hhv := dks.HHV(p).Float64()
	llv := dks.LLV(p).Float64()
	mid := (hhv + llv) / 2
	if mid <= 0 {
		return false
	}
	return dks[len(dks)-1].Close.Float64() >= mid
}

// ----------------------------------------------------------------------------
// 6. ARSI恢复 — RSI 从超卖回升至中性区卖出
// ----------------------------------------------------------------------------

// ARSI恢复 是 RSI 回升至中性区的卖出策略。
// Period RSI 周期，默认 14。
// Threshold RSI 卖出阈值，默认 50。
// 触发条件：RSI >= Threshold。
type ARSI恢复 struct {
	Period    int
	Threshold float64
}

func (s ARSI恢复) Name() string {
	p := s.Period
	if p == 0 {
		p = 14
	}
	t := s.Threshold
	if t == 0 {
		t = 50
	}
	return fmt.Sprintf("RSI%d>=%.0f", p, t)
}

func (s ARSI恢复) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	p := s.Period
	if p == 0 {
		p = 14
	}
	t := s.Threshold
	if t == 0 {
		t = 50
	}
	if len(dks) < p+1 {
		return false
	}
	// 用 extend.Klines 自带的 RSI（int64 0-100）
	rsi := float64(dks.RSI(p))
	return rsi >= t
}
