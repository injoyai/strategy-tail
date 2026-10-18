package sell

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/rule"
)

// ----------------------------------------------------------------------------
// 均值回归卖出策略族
// 接口：Sell(code string, dks extend.Klines, buy core.Buy) bool
// dks 包含到"今天"为止的全部 K 线（含买入日之前的历史数据）。
// 回测引擎保证：买入日当天不会调用 Sell，故 Sell 判断 dks 最后一天即可。
// ----------------------------------------------------------------------------

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
	// 判断委托共用内核，与旧实现同序：先均线守卫（MA>0，数据不足或零值
	// 均线时不成立，等价旧 len<p 与 ma<=0 提前返回），再今收 >= 布林中轨
	//（该周期均线即中轨，含恰等边界）。
	guard, err := rule.NewCompare(rule.MAValue(p), rule.Gt, rule.Literal(rule.FloatValue(0)))
	if err != nil {
		return false
	}
	main, err := rule.NewCompare(rule.CloseFloat(), rule.Ge, rule.MAValue(p))
	if err != nil {
		return false
	}
	and, err := rule.NewAnd(guard, main)
	if err != nil {
		return false
	}
	return and.Eval(rule.NewInput(code, dks, nil, &buy))
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
	// 判断委托共用内核，与旧实现同序：先均线守卫（MA>0，数据不足或零值
	// 均线时不成立，等价旧 len<p 与 ma<=0 提前返回），再今收 >= 均线
	//（含恰等边界）。
	guard, err := rule.NewCompare(rule.MAValue(p), rule.Gt, rule.Literal(rule.FloatValue(0)))
	if err != nil {
		return false
	}
	main, err := rule.NewCompare(rule.CloseFloat(), rule.Ge, rule.MAValue(p))
	if err != nil {
		return false
	}
	and, err := rule.NewAnd(guard, main)
	if err != nil {
		return false
	}
	return and.Eval(rule.NewInput(code, dks, nil, &buy))
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
	// 判断委托共用内核，与旧实现同序：先均线守卫（MA>0，数据不足或零值
	// 均线时不成立，等价旧 len<p 与 ma<=0 提前返回），再乖离率 >= 0。
	// 乖离率经 rule.BiasPercent 沿用旧合同公式 (Close-MA)/MA×100 的原始
	// 运算次序，恰等 0 边界与旧实现逐位一致。
	guard, err := rule.NewCompare(rule.MAValue(p), rule.Gt, rule.Literal(rule.FloatValue(0)))
	if err != nil {
		return false
	}
	main, err := rule.NewCompare(rule.BiasPercent(p), rule.Ge, rule.Literal(rule.FloatValue(0)))
	if err != nil {
		return false
	}
	and, err := rule.NewAnd(guard, main)
	if err != nil {
		return false
	}
	return and.Eval(rule.NewInput(code, dks, nil, &buy))
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
	// 判断委托共用内核：Z 值 >= 0（含恰等边界）。旧实现无均线守卫，
	// 与旧一致地不设：数据不足（len<p）或 σ=0（平价行情）时 Z 为无效值
	// 不触发，等价旧 NaN>=0 为 false。
	c, err := rule.NewCompare(rule.ZScore(p), rule.Ge, rule.Literal(rule.FloatValue(0)))
	if err != nil {
		return false
	}
	return c.Eval(rule.NewInput(code, dks, nil, &buy))
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
	// 判断委托共用内核：中轨 =（近 p 日最高价 + 近 p 日最低价）/2，窗口
	// 含今日（台账风险 §5.5 卖出口径，与买入侧唐奇安下沿的"不含今日"
	// 相反），窗口语义委托 HighestValue/LowestValue 操作数。内核无算术
	// DSL，中轨派生经 OperandFunc（与买入侧 ATR 占比同模式）：极值缺
	// 数据或中轨 <=0（全零行情）为无效值不触发，等价旧 len<p 与 mid<=0
	// 提前返回；今收 >= 中轨含恰等边界。
	hhv := rule.HighestValue(p, true)
	llv := rule.LowestValue(p, true)
	mid := rule.OperandFunc("唐奇安中轨(含今日)", func(in rule.Input) rule.Value {
		h, l := hhv.Value(in), llv.Value(in)
		if !h.Valid() || !l.Valid() {
			return rule.InvalidValue()
		}
		m := (h.Float() + l.Float()) / 2
		if m <= 0 {
			return rule.InvalidValue()
		}
		return rule.FloatValue(m)
	})
	c, err := rule.NewCompare(rule.CloseFloat(), rule.Ge, mid)
	if err != nil {
		return false
	}
	return c.Eval(rule.NewInput(code, dks, nil, &buy))
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
	// 判断委托共用内核：整数 RSI（extend 0-100 截断口径）>= 阈值（含
	// 恰等边界）。与买入侧浮点 RSI 不同源（台账风险 §5.4），迁移保持
	// 各自口径不混用；数据不足（len<p+1）时为无效值不触发，等价旧
	// len<p+1 预检查。
	c, err := rule.NewCompare(rule.RSIInt(p), rule.Ge, rule.Literal(rule.FloatValue(t)))
	if err != nil {
		return false
	}
	return c.Eval(rule.NewInput(code, dks, nil, &buy))
}
