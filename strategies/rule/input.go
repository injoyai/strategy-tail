// Package rule 提供买入、卖出条件共用的规则求值内核（迁移计划 T1）。
//
// 能力边界：
//   - 两域值：浮点元价格（float64）与浮点（元/比率/百分数），
//     跨值域不比较、不发生有损转换；
//   - 原语：比较（>/>=/</<=/==，含阈值相等边界）、区间（含/不含边界可配置，
//     结构性无界替代旧组件 ±1e9 哨兵与 Max=0 退化）、And/Or（短路、左到右）、
//     显式常量谓词；
//   - 适配器：BuyerAdapter/SellerAdapter 实现 core.Buyer/core.Seller；
//
// 硬约束：
//   - 规则对象构造期校验、构造后不可变；求值无副作用、无跨调用缓存；
//   - 每次求值只读取 Input 可见世界（代码、K 线前缀、AsOf、PIT 视图、持仓），
//     规则不得修改输入、不得读取可见前缀之后的数据；
//   - 无效值（缺数据/NaN/±Inf）不触发任何比较、不补 0（计划 3.4）；
//   - 不解析字符串公式、不做算术 DSL、无 NOT；时序与持仓原语由 T3/T4 追加，
//     Input 已预留持仓与 PIT 字段，无需返工；
//   - 依赖方向固定为 rule → core、strategies/factor、lib/extend、
//     researchdata、stdlib，禁止导入 common、strategies/buy、
//     strategies/sell 与 internal/*；factor 仅以操作数胶水方式
//     委托（operand_factor.go / operand_kline.go），不反向依赖。
package rule

import (
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/researchdata"
)

// Input 承载一次规则求值的可见世界。每次 Buy/Sell 调用由适配器构造一次，
// 规则求值只读本结构，不得修改任何字段指向的数据（K 线前缀与持仓为共享数据）。
type Input struct {
	// Code 股票代码。
	Code string
	// Klines 可见 K 线前缀（截至当前求值时刻，含末根），只读；
	// 规则不得修改元素，也不得取前缀之后的数据。
	Klines extend.Klines
	// Data PIT 数据视图，可空（nil 表示调用方未提供数据视图）。
	// 复用 core.FactorContext / core.ContextFactor 的既有 PIT 语义，
	// 规则侧不另行发明数据访问接口。
	Data researchdata.View
	// Buy 持仓信息，可空。买入侧求值恒为 nil；卖出侧由 Seller 适配器传入。
	Buy *core.Buy
	// 仅供本次求值及其截断前缀复用；NewInput 每次创建独立作用域。
	macd *macdScope
}

// NewInput 构造一次求值的可见世界；data 与 buy 均可为 nil。
// 买入侧必须传 buy=nil：需要持仓的原语遇到不可用持仓时按不触发处理，
// 不得伪造买入价或买入时间。
// 截断前缀可共享 Input；行情或分钟快照改变后必须重新构造，不能复用旧作用域。
func NewInput(code string, dks extend.Klines, data researchdata.View, buy *core.Buy) Input {
	return Input{Code: code, Klines: dks, Data: data, Buy: buy, macd: &macdScope{klines: dks}}
}

// AsOf 本次可见快照的时点：末根 K 线的 Time，从可见快照派生，
// 不使用系统当前时间。空前缀返回零值时间。
// 与 core.BindContextFactor 组装 FactorContext 的取值口径一致。
func (in Input) AsOf() time.Time {
	if len(in.Klines) == 0 {
		return time.Time{}
	}
	return in.Klines[len(in.Klines)-1].Time
}

// FactorContext 按 core 的 PIT 语义组装因子上下文，供 ContextFactor 取数；
// AsOf 由可见快照派生。Data 为 nil 时视图为空，因子按各自契约返回无效值。
func (in Input) FactorContext() core.FactorContext {
	return core.FactorContext{
		Code:   in.Code,
		AsOf:   in.AsOf(),
		Klines: in.Klines,
		Data:   in.Data,
	}
}

// Position 返回本次求值的持仓信息；第二返回值 false 表示持仓不可用
// （买入侧求值，或调用方未提供）。需要持仓的原语必须按不可用处理，
// 不得伪造买入价或买入时间；返回的持仓只读，不得修改。
func (in Input) Position() (*core.Buy, bool) {
	if in.Buy == nil {
		return nil, false
	}
	return in.Buy, true
}
