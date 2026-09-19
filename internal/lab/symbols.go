package lab

import (
	"reflect"

	"github.com/injoyai/strategy-tail/core"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
	f "github.com/injoyai/strategy-tail/strategies/factor"
	ss "github.com/injoyai/strategy-tail/strategies/sell"
	"github.com/traefik/yaegi/interp"
)

// symbols.go 项目包符号表：供策略脚本 import 项目现有组件。
//
// Yaegi binary 包机制：(*T)(nil) 注册类型符号、reflect.ValueOf(fn) 注册函数符号。
// 脚本内 `import "github.com/injoyai/strategy-tail/strategies/buy"` 会映射到
// key "github.com/injoyai/strategy-tail/strategies/buy/buy"（包路径 + 包名后缀）。
//
// 首批只注册回测脚本常用类型（探针已验证 A阴线收回/And/MAUp 可用）；
// 后续按需追加，踩雷类型记入 MEMORY.md。

// ProjectSymbols 项目包符号表（供脚本 import）。
func ProjectSymbols() interp.Exports {
	return interp.Exports{
		"github.com/injoyai/strategy-tail/core/core": {
			// 契约类型
			"Variant": reflect.ValueOf((*core.Variant)(nil)),
		},
		"github.com/injoyai/strategy-tail/strategies/buy/buy": {
			// 组合子
			"And": reflect.ValueOf((*sb.And)(nil)),
			"Or":  reflect.ValueOf((*sb.Or)(nil)),
			"Not": reflect.ValueOf(sb.Not),
			// 形态/过滤
			"A阴线收回": reflect.ValueOf((*sb.A阴线收回)(nil)),
			"A价格":   reflect.ValueOf((*sb.A价格)(nil)),
			"A流通市值": reflect.ValueOf((*sb.A流通市值)(nil)),
			"A过滤涨停": reflect.ValueOf((*sb.A过滤涨停)(nil)),
			// 因子策略（Task 6/7）
			"A因子过滤":   reflect.ValueOf((*sb.A因子过滤)(nil)),
			"A因子TopN": reflect.ValueOf((*sb.A因子TopN)(nil)),
			// 趋势
			"MAUp":    reflect.ValueOf((*sb.MAUp)(nil)),
			"A均线多头排列": reflect.ValueOf((*sb.A均线多头排列)(nil)),
		},
		"github.com/injoyai/strategy-tail/strategies/sell/sell": {
			// 组合子
			"And": reflect.ValueOf((*ss.And)(nil)),
			"Or":  reflect.ValueOf((*ss.Or)(nil)),
			// 规则
			"A持仓N天": reflect.ValueOf((*ss.A持仓N天)(nil)),
			"A止盈止损": reflect.ValueOf((*ss.A止盈止损)(nil)),
		},
		"github.com/injoyai/strategy-tail/strategies/factor/factor": {
			// 因子：值接收者方法集，脚本内值字面量即可实现 core.Factor。
			// 仅拉丁大写开头类型可跨包注册；其余 14 个因子（原 8 个及新增的
			// 6 个 MACDBuyer 映射因子）以汉字开头未导出，
			// 包外不可见（Go 导出规则：首字符须属 Unicode Lu 类别），
			// 脚本侧经下方 Build(kind) 使用。
			"N日动量":  reflect.ValueOf((*f.N日动量)(nil)),
			"N日斜率":  reflect.ValueOf((*f.N日斜率)(nil)),
			"N日波动":  reflect.ValueOf((*f.N日波动)(nil)),
			"N日振幅":  reflect.ValueOf((*f.N日振幅)(nil)),
			"N日高低位": reflect.ValueOf((*f.N日高低位)(nil)),
			"K值":    reflect.ValueOf((*f.K值)(nil)),
			// 工厂：未知 kind 返回 nil；days<=0 用默认参数（全部 20 类因子的脚本入口）
			"Build": reflect.ValueOf(f.Build),
		},
	}
}
