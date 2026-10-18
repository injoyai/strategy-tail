package sell

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	f "github.com/injoyai/strategy-tail/strategies/factor"
	"github.com/injoyai/strategy-tail/strategies/rule"
)

// A跌破短期均线 是收盘价跌破短期均线即触发的卖出条件。
// Period 表示均线周期，默认 10。
// 高胜率策略的核心卖出条件：回调买入后，如果跌破短期均线说明趋势可能反转。
// 比"跌破MA20"更敏感，能更快止损。
type A跌破短期均线 struct {
	Period int
}

func (s A跌破短期均线) Name() string {
	period := s.Period
	if period == 0 {
		period = 10
	}
	return fmt.Sprintf("跌破MA%d", period)
}

func (s A跌破短期均线) Sell(code string, dks extend.Klines, b core.Buy) bool {
	period := s.Period
	if period == 0 {
		period = 10
	}
	// 判断委托共用内核：今收 < 该周期均线（严格，恰等不触发）。旧实现无
	// MA>0 守卫，迁移同样不设：数据不足时均线为无效值不触发，等价旧
	// len<period 预检查，不引入额外门槛或守卫改变边界行为。
	c, err := rule.NewCompare(rule.CloseFloat(), rule.Lt, rule.MAValue(period))
	if err != nil {
		return false
	}
	return c.Eval(rule.NewInput(code, dks, nil, &b))
}

// A固定止盈 是盈利达到目标即卖出的保守止盈条件。
// Pct 表示止盈百分比（小数），默认 0.05（5%）。
// 高胜率策略的核心：不贪婪，达到目标就锁定利润。
type A固定止盈 float64

func (s A固定止盈) Name() string {
	pct := s
	if pct == 0 {
		pct = 0.05
	}
	return fmt.Sprintf("止盈%.1f%%", pct*100)
}

func (s A固定止盈) Sell(code string, dks extend.Klines, b core.Buy) bool {
	pct := float64(s)
	if pct == 0 {
		pct = 0.05
	}
	// 判断委托共用内核：持仓收益率 >= 阈值（恰等触发），与 A盈利 同构。
	// 旧 len==0 与 buy<=0 预检查由收益率无效值等价表达。
	c, err := rule.NewCompare(rule.PositionProfitPercent(), rule.Ge, rule.Literal(rule.FloatValue(pct)))
	if err != nil {
		return false
	}
	return c.Eval(rule.NewInput(code, dks, nil, &b))
}

// A_ATR止盈 是基于ATR的保守止盈条件。
// Period 表示ATR周期，默认 14。
// Multiple 表示止盈ATR倍数，默认 2.0。
// 到达目标后立即卖出，不等待更多利润。
type A_ATR止盈 struct {
	Period   int
	Multiple float64
}

func (s A_ATR止盈) Name() string {
	mul := s.Multiple
	if mul == 0 {
		mul = 2.0
	}
	return fmt.Sprintf("ATR止盈x%.1f", mul)
}

func (s A_ATR止盈) Sell(code string, dks extend.Klines, b core.Buy) bool {
	period := s.Period
	if period == 0 {
		period = 14
	}
	mul := s.Multiple
	if mul == 0 {
		mul = 2.0
	}
	// 判断委托共用内核：今收 >= 买入价 + mul*ATR（恰等触发）。阈值线经
	// 操作数逃逸口表达（内核无算术）：ATR 无效（含 n<period+1 数据不足，
	// factor 返回 NaN）或买入价非正时阈值线无效不触发，等价旧预检查；
	// 运算次序保持 buy + mul*atr 与旧实现一致。
	atrOp := rule.ATR(period)
	buyOp := rule.PositionBuyPriceFloat()
	atrLine := rule.OperandFunc("ATR止盈线", func(in rule.Input) rule.Value {
		atr := atrOp.Value(in)
		buy := buyOp.Value(in)
		if !atr.Valid() || !buy.Valid() {
			return rule.InvalidValue()
		}
		return rule.FloatValue(buy.Float() + mul*atr.Float())
	})
	c, err := rule.NewCompare(rule.CloseFloat(), rule.Ge, atrLine)
	if err != nil {
		return false
	}
	return c.Eval(rule.NewInput(code, dks, nil, &b))
}

// A均线死叉 是短期均线下穿长期均线的卖出条件。
// Short/Long 表示均线周期，默认 5/20。
// 比"跌破均线"更可靠的趋势反转信号。
type A均线死叉 struct {
	Short int
	Long  int
}

func (s A均线死叉) Name() string {
	short, long := s.params()
	return fmt.Sprintf("MA%d下穿MA%d", short, long)
}

func (s A均线死叉) params() (int, int) {
	short, long := s.Short, s.Long
	if short == 0 {
		short = 5
	}
	if long == 0 {
		long = 20
	}
	return short, long
}

func (s A均线死叉) Sell(code string, dks extend.Klines, b core.Buy) bool {
	short, long := s.params()
	// 判断委托共用内核：昨短>=昨长（恰等在昨日侧，CrossDown 不含今日口径）
	// 且 今短<今长（严格）。旧 len<long+1 预检查由均线无效值等价表达：
	// n==long 时昨侧均线（前缀 n-1<long）无效不触发，n<long 时今侧无效，
	// 不引入额外门槛。
	cross, err := rule.CrossDown(rule.MAValue(short), rule.MAValue(long), false)
	if err != nil {
		return false
	}
	return cross.Eval(rule.NewInput(code, dks, nil, &b))
}

// A缩量滞涨 是成交量萎缩且涨幅极小的卖出条件（动能衰竭信号）。
// VolumePeriod 表示均量周期，默认 5。
// VolumeRatio 表示今日量/均量的阈值，默认 0.6（缩量）。
// MaxRise 表示允许的最大涨幅，默认 0.5（%）。
// Lookback 表示连续滞涨天数，默认 3。
// 说明上涨动能衰竭，趋势可能即将反转。
type A缩量滞涨 struct {
	VolumePeriod int
	VolumeRatio  float64
	MaxRise      float64
	Lookback     int
}

func (s A缩量滞涨) Name() string {
	return "缩量滞涨"
}

func (s A缩量滞涨) Sell(code string, dks extend.Klines, b core.Buy) bool {
	volPeriod := s.VolumePeriod
	if volPeriod == 0 {
		volPeriod = 5
	}
	volRatio := s.VolumeRatio
	if volRatio == 0 {
		volRatio = 0.6
	}
	maxRise := s.MaxRise
	if maxRise == 0 {
		maxRise = 0.5
	}
	lookback := s.Lookback
	if lookback == 0 {
		lookback = 3
	}
	n := len(dks)
	if n < volPeriod+lookback {
		return false
	}
	// 连续N天缩量滞涨
	count := 0
	for i := n - lookback; i < n; i++ {
		// 缩量
		avg := f.A前N日均量(volPeriod).Value(code, dks[:i+1])
		if avg <= 0 || float64(dks[i].Volume) > avg*volRatio {
			continue
		}
		// 滞涨（涨幅极小）
		if dks[i].RiseRate() <= maxRise {
			count++
		}
	}
	return count >= lookback
}
