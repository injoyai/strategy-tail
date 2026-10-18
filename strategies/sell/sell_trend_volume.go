package sell

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	f "github.com/injoyai/strategy-tail/strategies/factor"
	"github.com/injoyai/strategy-tail/strategies/rule"
)

// A收盘跌破均线 是收盘价连续N日跌破指定均线的卖出条件。
// Period 表示均线周期，默认 20。
// Days 表示连续跌破的天数，默认 3。
// 当最近 Days 天收盘价都低于或等于均线时返回卖出信号。
type A收盘跌破均线 struct {
	Period int
	Days   int
}

func (s A收盘跌破均线) Name() string {
	period := s.Period
	if period == 0 {
		period = 20
	}
	days := s.Days
	if days == 0 {
		days = 3
	}
	return fmt.Sprintf("跌破%d日均线%d日未收回", period, days)
}

func (s A收盘跌破均线) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	period := s.Period
	if period == 0 {
		period = 20
	}
	days := s.Days
	if days == 0 {
		days = 3
	}
	// 判断委托共用内核，与旧实现同序：先预热拦截（n<period+days，比连续段
	// 自身"均线无效即断"的拦截严一天，必须显式 MinKlines 表达），再连续
	// days 天今收<=当日均线（MA下方连续天数的含相等口径）。旧比较为
	// count>=days 无上限，Consecutive 上限传 0（上限不限），内核统计到
	// MinDays 即停，判定等价。
	gate, err := rule.NewMinKlines(period + days)
	if err != nil {
		return false
	}
	below, err := rule.NewCompare(rule.CloseFloat(), rule.Le, rule.MAValue(period))
	if err != nil {
		return false
	}
	streak, err := rule.NewConsecutive(days, 0, below, true)
	if err != nil {
		return false
	}
	and, err := rule.NewAnd(gate, streak)
	if err != nil {
		return false
	}
	return and.Eval(rule.NewInput(code, dks, nil, &buy))
}

// A_RSI超买 是 RSI 大于指定阈值的卖出条件。
// Period 表示 RSI 计算周期，默认 14。
// Threshold 表示超买阈值，默认 75。
type A_RSI超买 struct {
	Period    int
	Threshold float64
}

func (s A_RSI超买) Name() string {
	threshold := s.Threshold
	if threshold == 0 {
		threshold = 75
	}
	return fmt.Sprintf("RSI>%.0f", threshold)
}

func (s A_RSI超买) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	period := s.Period
	if period == 0 {
		period = 14
	}
	threshold := s.Threshold
	if threshold == 0 {
		threshold = 75
	}
	// 判断委托共用内核：浮点 RSI（util.CalcRSI 口径，与买入侧同源）
	// > 阈值（严格大于，恰等不触发）。RSI 数据不足（len<period+1）时
	// 为无效值不触发，等价旧预检查；本组件用浮点 RSI，与 ARSI恢复 的
	// 整数 RSI 不同源（台账风险 §5.4），迁移保持各自口径不混用。
	c, err := rule.NewCompare(rule.RSIFloat(period), rule.Gt, rule.Literal(rule.FloatValue(threshold)))
	if err != nil {
		return false
	}
	return c.Eval(rule.NewInput(code, dks, nil, &buy))
}

// A_MACD死叉且DIF为负 是 MACD 死叉同时 DIF 小于 0 的卖出条件。
// Fast/Slow/Signal 为 MACD 参数，默认 12/26/9。
type A_MACD死叉且DIF为负 struct {
	Fast   int
	Slow   int
	Signal int
}

func (s A_MACD死叉且DIF为负) Name() string {
	return "MACD死叉且DIF<0"
}

func (s A_MACD死叉且DIF为负) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	fast, slow, signal := defaultMACDParams(s.Fast, s.Slow, s.Signal)
	// 判断委托共用内核，与旧实现同序：先预热拦截（n<slow+signal，MACD 柱
	// 对短前缀仍给出有限值，必须显式 MinKlines），再零轴下穿（昨柱>0 且
	// 今柱<=0，CrossDown 恰等在今日侧），最后 DIF<0（严格）。DIF 经
	// OperandFunc 委托 factor.MACDDIF值，不在卖出侧复制公式；旧
	// len(hist)!=n 防御在内核按前缀求值下天然成立。
	gate, err := rule.NewMinKlines(slow + signal)
	if err != nil {
		return false
	}
	cross, err := rule.CrossDown(rule.MACDHist(fast, slow, signal), rule.Literal(rule.FloatValue(0)), true)
	if err != nil {
		return false
	}
	dif := f.MACDDIF值{Fast: fast, Slow: slow}
	difNeg, err := rule.NewCompare(
		rule.OperandFunc(dif.Name(), func(in rule.Input) rule.Value {
			return rule.FloatValue(dif.Value(in.Code, in.Klines))
		}),
		rule.Lt, rule.Literal(rule.FloatValue(0)))
	if err != nil {
		return false
	}
	and, err := rule.NewAnd(gate, cross, difNeg)
	if err != nil {
		return false
	}
	return and.Eval(rule.NewInput(code, dks, nil, &buy))
}

// A单日跌幅大于 是单日跌幅超过指定值的卖出条件。
// Max 表示触发卖出的跌幅阈值（%），默认 7。
// 例如 Max=7 表示当日跌幅超过 7% 时返回卖出信号。
type A单日跌幅大于 struct {
	Max float64
}

func (s A单日跌幅大于) Name() string {
	max := s.Max
	if max == 0 {
		max = 7
	}
	return fmt.Sprintf("单日跌幅>%.0f%%", max)
}

func (s A单日跌幅大于) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	max := s.Max
	if max == 0 {
		max = 7
	}
	// 判断委托共用内核：当日涨幅（行情 RiseRate 口径，Last=0 退化对
	// 开盘价）< -max（严格小于，恰等不触发）。涨幅为 NaN/±Inf（畸形
	// Last=0 且 Open=0）或空行情时为无效值不触发，等价旧 len==0 预检查
	// 与 NaN 比较为 false。
	c, err := rule.NewCompare(rule.RiseRate(), rule.Lt, rule.Literal(rule.FloatValue(-max)))
	if err != nil {
		return false
	}
	return c.Eval(rule.NewInput(code, dks, nil, &buy))
}

// A时间止损 是持有时间到期且未达到最低盈利的卖出条件。
// MaxHoldDays 表示最大持有天数，默认 20。
// MinProfitRate 表示最低盈利比例（小数），默认 0.05（5%）。
// 当持有天数 >= MaxHoldDays 且收益率 <= MinProfitRate 时返回卖出信号。
type A时间止损 int

func (s A时间止损) Name() string {
	days := s
	if days == 0 {
		days = 20
	}
	return fmt.Sprintf("持有%d日", days)
}

func (s A时间止损) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	maxDays := s
	if maxDays == 0 {
		maxDays = 20
	}
	n := len(dks)
	if n == 0 {
		return false
	}
	buyPrice := buy.Price.Float64()
	if buyPrice <= 0 {
		return false
	}
	// 计算持有天数（按K线数）
	holdDays := 0
	for i := n - 1; i >= 0; i-- {
		if !dks[i].Time.After(buy.Time) {
			break
		}
		holdDays++
	}
	return holdDays >= int(maxDays)
}

// defaultMACDParams 返回默认的 MACD 参数（12, 26, 9）
func defaultMACDParams(fast, slow, signal int) (int, int, int) {
	if fast == 0 {
		fast = 12
	}
	if slow == 0 {
		slow = 26
	}
	if signal == 0 {
		signal = 9
	}
	return fast, slow, signal
}

// A固定止损 是相对买入价跌幅超过阈值的卖出条件。
// Pct 表示止损百分比，默认 0.05（即-5%）。
// 立即触发，无延迟。建议作为第一道防线。
type A固定止损 struct {
	Pct float64
}

func (s A固定止损) Name() string {
	pct := s.Pct
	if pct == 0 {
		pct = 0.05
	}
	return fmt.Sprintf("固定止损%.1f%%", pct*100)
}

func (s A固定止损) Sell(code string, dks extend.Klines, b core.Buy) bool {
	pct := s.Pct
	if pct == 0 {
		pct = 0.05
	}
	// 判断委托共用内核：持仓收益率 <= -pct（恰等触发）。旧 len==0 与
	// buy<=0 预检查由收益率无效值等价表达；公式次序与旧实现一致。
	c, err := rule.NewCompare(rule.PositionProfitPercent(), rule.Le, rule.Literal(rule.FloatValue(-pct)))
	if err != nil {
		return false
	}
	return c.Eval(rule.NewInput(code, dks, nil, &b))
}

// A_ATR止损 是基于ATR动态止损的卖出条件。
// Period 表示ATR周期，默认14。
// Multiple 表示止损倍数，默认2.5（最大回撤=2.5*ATR）。
// 适应不同波动性的股票，比固定百分比止损更科学。
type A_ATR止损 struct {
	Period   int
	Multiple float64
}

func (s A_ATR止损) Name() string {
	mul := s.Multiple
	if mul == 0 {
		mul = 2.5
	}
	return fmt.Sprintf("ATR止损x%.1f", mul)
}

func (s A_ATR止损) Sell(code string, dks extend.Klines, b core.Buy) bool {
	period := s.Period
	if period == 0 {
		period = 14
	}
	mul := s.Multiple
	if mul == 0 {
		mul = 2.5
	}
	// 判断委托共用内核：今收 < 买入价 - mul*ATR（严格）。阈值线经操作数
	// 逃逸口表达（内核无算术）：ATR 无效（含 n<period+1 数据不足，
	// factor 返回 NaN）或买入价非正时无效不触发，等价旧预检查；
	// 运算次序保持 buy - mul*atr 与旧实现一致。
	atrOp := rule.ATR(period)
	buyOp := rule.PositionBuyPriceFloat()
	atrLine := rule.OperandFunc("ATR止损线", func(in rule.Input) rule.Value {
		atr := atrOp.Value(in)
		buy := buyOp.Value(in)
		if !atr.Valid() || !buy.Valid() {
			return rule.InvalidValue()
		}
		return rule.FloatValue(buy.Float() - mul*atr.Float())
	})
	c, err := rule.NewCompare(rule.CloseFloat(), rule.Lt, atrLine)
	if err != nil {
		return false
	}
	return c.Eval(rule.NewInput(code, dks, nil, &b))
}

// A移动止盈 是盈利达到一定水平后回吐部分利润触发的卖出条件。
// ActivateProfitPct 表示激活移动止盈的最低盈利（小数），默认0.05（5%）。
// RetreatPct 表示从最高点回撤的比例，默认0.5（即回吐50%利润）。
// 例如：盈利10%后回撤至5%（10%×0.5）即卖出。
type A移动止盈 struct {
	ActivateProfitPct float64
	RetreatPct        float64
}

func (s A移动止盈) Name() string {
	return "移动止盈"
}

func (s A移动止盈) Sell(code string, dks extend.Klines, b core.Buy) bool {
	activate := s.ActivateProfitPct
	if activate == 0 {
		activate = 0.05
	}
	retreat := s.RetreatPct
	if retreat == 0 {
		retreat = 0.5
	}
	// 判断委托共用内核：持仓收益率 <= 移动止盈回吐线（恰等触发）。
	// 回吐线经操作数逃逸口表达（内核无窗口极值算术）：自买入时刻起
	// （含买入日当天）回扫日内最高收益，只取正收益；峰值低于激活线时
	// 线无效不触发（等价旧 maxProfit<activate 返回 false），持仓不可用
	// /买入价非正/空前缀同样无效，等价旧预检查。运算次序
	// maxProfit*(1-retreat) 与旧实现一致。
	buyOp := rule.PositionBuyPriceFloat()
	line := rule.OperandFunc("移动止盈回吐线", func(in rule.Input) rule.Value {
		pos, ok := in.Position()
		if !ok {
			return rule.InvalidValue()
		}
		buy := buyOp.Value(in)
		if !buy.Valid() || len(in.Klines) == 0 {
			return rule.InvalidValue()
		}
		buyPrice := buy.Float()
		maxProfit := 0.0
		for i := len(in.Klines) - 1; i >= 0; i-- {
			if in.Klines[i].Time.Before(pos.Time) {
				break
			}
			profit := (in.Klines[i].High.Float64() - buyPrice) / buyPrice
			if profit > maxProfit {
				maxProfit = profit
			}
		}
		if maxProfit < activate {
			return rule.InvalidValue()
		}
		return rule.FloatValue(maxProfit * (1 - retreat))
	})
	c, err := rule.NewCompare(rule.PositionProfitPercent(), rule.Le, line)
	if err != nil {
		return false
	}
	return c.Eval(rule.NewInput(code, dks, nil, &b))
}

// A放量大阴线 是当日放量下跌的卖出条件（"出货线"）。
// VolumeRatio 表示成交量放大倍数（相对前5日均量），默认1.5。
// FallPct 表示最低跌幅，默认2.0（%）。
// 主力出货信号，应立即离场。
type A放量大阴线 struct {
	VolumeRatio float64
	FallPct     float64
}

func (s A放量大阴线) Name() string {
	return "放量大阴线"
}

func (s A放量大阴线) Sell(code string, dks extend.Klines, b core.Buy) bool {
	volRatio := s.VolumeRatio
	if volRatio == 0 {
		volRatio = 1.5
	}
	fallPct := s.FallPct
	if fallPct == 0 {
		fallPct = 2.0
	}
	// 判断委托共用内核，与旧实现同序短路：先阴线（今收 < 今开，厘级
	// 整数比较，等价旧 Close>=Open 提前返回；open==close 平盘不触发），
	// 再跌幅（当日涨幅 <= -fallPct，恰等含边界），最后量比（今日量 >
	// 前5日均量(不含今日)×倍数，严格大于）。基准均量缺失或 <=0 为无效
	// 值不触发，等价旧 avgVol<=0 提前返回；n<6 由量操作数自身的无效值
	// 兜住（A前N日均量 需 6 根）。
	bear, err := rule.NewCompare(rule.ClosePrice(), rule.Lt, rule.OpenPrice())
	if err != nil {
		return false
	}
	fall, err := rule.NewCompare(rule.RiseRate(), rule.Le, rule.Literal(rule.FloatValue(-fallPct)))
	if err != nil {
		return false
	}
	vol, err := rule.NewCompare(rule.Volume(), rule.Gt, rule.PrevAvgVolumeScaled(5, volRatio))
	if err != nil {
		return false
	}
	and, err := rule.NewAnd(bear, fall, vol)
	if err != nil {
		return false
	}
	return and.Eval(rule.NewInput(code, dks, nil, &b))
}
