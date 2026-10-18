package buy

import (
	"fmt"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/rule"
)

// RSI 是 RSI 超卖买入策略。
// Period 表示 RSI 计算周期，默认 14。
// Threshold 表示买入阈值，默认 20。
// 当最新一个交易日 RSI 小于 Threshold 时返回买入信号。
// 买入价使用最新交易日收盘价。
type RSI struct {
	Period    int
	Threshold float64
}

func (s RSI) Name() string {
	return "RSI"
}

func (s RSI) Buy(code string, dks extend.Klines) bool {

	if s.Period == 0 {
		s.Period = 14
	}
	if s.Threshold == 0 {
		s.Threshold = 20
	}

	// 判断委托共用内核：RSI(浮点，util.CalcRSI 口径) < threshold（严格）。
	// RSI 缺数据（len<period+1，因子返回 NaN）为无效值不触发，等价旧
	// 预热守卫；窗口充足时 util.CalcRSI 恒为 0..100 有限值（全平价=50、
	// 纯涨=100），不存在旧代码字面上的 NaN 分支差异。
	cond, err := rule.NewCompare(rule.RSIFloat(s.Period), rule.Lt, rule.Literal(rule.FloatValue(s.Threshold)))
	if err != nil {
		return false
	}
	return cond.Eval(rule.NewInput(code, dks, nil, nil))
}

// RSI拐头 是 RSI 动量反转确认策略。
// Period 表示 RSI 计算周期，默认 14。
// 触发条件：今日 RSI > 昨日 RSI，即 RSI 从下行转为上行，确认超卖反转启动。
// 通常与 RSI 超卖策略组合使用（And{RSI{}, RSI拐头{}}），
// 先确认 RSI 处于超卖区，再确认拐头向上，避免在持续下跌中接飞刀。
type RSI拐头 struct {
	Period int
}

func (s RSI拐头) Name() string {
	p := s.Period
	if p == 0 {
		p = 14
	}
	return fmt.Sprintf("RSI%d拐头", p)
}

func (s RSI拐头) Buy(code string, dks extend.Klines) bool {
	p := s.Period
	if p == 0 {
		p = 14
	}
	// 判断委托共用内核，与旧实现同构：拐头需今日与昨日两份 RSI，
	// 旧预热守卫拒绝 len<p+2（比逐份计算所需的 p+1 严一天），显式
	// MinKlines(p+2) 表达；昨日 RSI 用 RefOperand 按截断前缀求值，
	// 与旧 dks[:len-1] 口径一致。RSI 缺数据（len<p+1）因子返回 NaN
	// 为无效值不触发，与旧 NaN>NaN 为 false 等价。
	gate, err := rule.NewMinKlines(p + 2)
	if err != nil {
		return false
	}
	prev, err := rule.RefOperand(1, rule.RSIFloat(p))
	if err != nil {
		return false
	}
	turnUp, err := rule.NewCompare(rule.RSIFloat(p), rule.Gt, prev)
	if err != nil {
		return false
	}
	and, err := rule.NewAnd(gate, turnUp)
	if err != nil {
		return false
	}
	return and.Eval(rule.NewInput(code, dks, nil, nil))
}
