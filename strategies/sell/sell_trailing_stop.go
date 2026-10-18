package sell

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/rule"
)

// A追踪止损 从买入后最高收盘价回撤达阈值时卖出的追踪止损策略。
// Drawdown 表示回撤比例（相对峰值），例如 0.05 表示从最高收盘价回撤 5% 触发。
//
// 无状态：扫描 dks 中买入日（含）至今天的收盘价找出峰值 peak，
// 计算当前收盘价相对 peak 的回撤 (peak-current)/peak，回撤 >= Drawdown 则触发。
// 只使用买入日之后的数据，不会有前视偏差。
type A追踪止损 struct {
	Drawdown float64
}

func (s A追踪止损) Name() string {
	d := s.Drawdown
	if d == 0 {
		d = 0.05
	}
	return fmt.Sprintf("追踪止损%.0f%%", d*100)
}

func (s A追踪止损) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	if s.Drawdown <= 0 {
		return false
	}
	// 判断委托共用内核：买入以来峰值回撤 >= Drawdown（恰等触发）。
	// 回撤经操作数逃逸口表达（内核无窗口极值算术）：峰值扫描买入日
	// （含）至今；买入日定位沿用旧口径顺序日期串匹配（计划第 4 节
	// 不统一各组件定位算法）；peak<=0、找不到买入日、持仓不可用、
	// 买入价非正、空前缀一律无效不触发，等价旧预检查。运算次序
	// (peak-current)/peak 与旧实现一致。
	drawdown := rule.OperandFunc("买入以来峰值回撤", func(in rule.Input) rule.Value {
		n := len(in.Klines)
		if n == 0 {
			return rule.InvalidValue()
		}
		pos, ok := in.Position()
		if !ok {
			return rule.InvalidValue()
		}
		if pos.Price.Float64() <= 0 {
			return rule.InvalidValue()
		}
		buyIdx := -1
		buyDate := pos.Time.Format("2006-01-02")
		for i, k := range in.Klines {
			if k.Time.Format("2006-01-02") == buyDate {
				buyIdx = i
				break
			}
		}
		if buyIdx < 0 {
			return rule.InvalidValue()
		}
		// 扫描买入日（含）到今天的最高收盘价
		peak := 0.0
		for i := buyIdx; i < n; i++ {
			c := in.Klines[i].Close.Float64()
			if c > peak {
				peak = c
			}
		}
		if peak <= 0 {
			return rule.InvalidValue()
		}
		current := in.Klines[n-1].Close.Float64()
		return rule.FloatValue((peak - current) / peak)
	})
	c, err := rule.NewCompare(drawdown, rule.Ge, rule.Literal(rule.FloatValue(s.Drawdown)))
	if err != nil {
		return false
	}
	return c.Eval(rule.NewInput(code, dks, nil, &buy))
}
