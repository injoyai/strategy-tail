package buy

import (
	"fmt"

	"github.com/injoyai/strategy-tail/lib/extend"
)

// A指数多头排列 以基准指数均线排列作为大盘环境过滤条件。
// Periods 表示从短到长的均线周期，默认 [20, 60, 120]。
// 只有当基准指数的 MA[0] > MA[1] > MA[2]（多头排列）时才允许策略开仓。
// Ks 为预加载的基准指数日线数据（如 sh000300 沪深300）。
// 典型用法：在 buy.And 链中作为第一个条件，大盘空头时直接拦截所有买入信号。
type A指数多头排列 struct {
	Ks      extend.Klines // 预加载的指数日线（需覆盖回测区间 + MA最大周期）
	Periods []int         // MA周期列表，从短到长，默认 [20, 60, 120]
}

func (b A指数多头排列) Name() string {
	periods := b.Periods
	if len(periods) == 0 {
		periods = []int{20, 60, 120}
	}
	return fmt.Sprintf("指数MA%v多头排列", periods)
}

func (b A指数多头排列) Buy(code string, dks extend.Klines) bool {
	periods := b.Periods
	if len(periods) == 0 {
		periods = []int{20, 60, 120}
	}
	if len(dks) == 0 || len(b.Ks) == 0 {
		return false
	}

	// 对齐日期：取不晚于当前个股日期的指数K线，避免未来函数
	t := dks[len(dks)-1].Time
	aligned := make(extend.Klines, 0, len(b.Ks))
	for _, k := range b.Ks {
		if k == nil {
			continue
		}
		if !k.Time.After(t) {
			aligned = append(aligned, k)
		}
	}

	// 确认有足够K线计算所有MA
	maxP := periods[0]
	for _, p := range periods {
		if p > maxP {
			maxP = p
		}
	}
	if len(aligned) < maxP {
		return false
	}

	// 检查均线多头排列：MA[0] > MA[1] > MA[2]
	for i := 0; i < len(periods)-1; i++ {
		short := aligned.MA(periods[i])
		long := aligned.MA(periods[i+1])
		if short <= long {
			return false
		}
	}
	return true
}

// A指数站上MA 检查基准指数收盘价是否站上N日均线。
// Period 为均线周期，默认 60。
// 用于确认指数短期价格在均线上方，与 A指数多头排列 组合使用效果更佳。
type A指数站上MA struct {
	Ks     extend.Klines
	Period int
}

func (b A指数站上MA) Name() string {
	period := b.Period
	if period == 0 {
		period = 60
	}
	return fmt.Sprintf("指数站上MA%d", period)
}

func (b A指数站上MA) Buy(code string, dks extend.Klines) bool {
	period := b.Period
	if period == 0 {
		period = 60
	}
	if len(dks) == 0 || len(b.Ks) == 0 {
		return false
	}

	t := dks[len(dks)-1].Time
	aligned := make(extend.Klines, 0, len(b.Ks))
	for _, k := range b.Ks {
		if k == nil {
			continue
		}
		if !k.Time.After(t) {
			aligned = append(aligned, k)
		}
	}
	if len(aligned) < period {
		return false
	}

	ma := aligned.MA(period)
	return aligned[len(aligned)-1].Close.Float64() > ma.Float64()
}
