package sell

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/rule"
)

type MACD struct {
	Fast   int
	Slow   int
	Signal int
}

// MACD反转 是 MACD 高位拐头卖出策略。
// Fast 表示快线 EMA 周期，默认 12。
// Slow 表示慢线 EMA 周期，默认 26。
// Signal 表示 DEA EMA 周期，默认 9。
// Lookback 表示向前比较的窗口长度，默认 20。
// MinDiff 表示今天 MACD 柱子必须比昨天至少小多少，默认 0。
// 卖出时会逐日遍历 future，找到第一次满足“昨天为近期高点且今天回落”的日期。
type MACD反转 struct {
	Fast     int
	Slow     int
	Signal   int
	Lookback int
	MinDiff  float64
}

func (s MACD反转) Name() string {
	return fmt.Sprintf("%d日MACD最高点后", s.Lookback)
}

func (s MACD反转) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	if s.Fast == 0 {
		s.Fast = 12
	}
	if s.Slow == 0 {
		s.Slow = 26
	}
	if s.Signal == 0 {
		s.Signal = 9
	}
	if s.Lookback == 0 {
		s.Lookback = 20
	}

	// 判断委托共用内核，与旧实现同序：先今柱 < 昨柱-MinDiff（严格小于，
	// MinDiff 减法派生留门面——内核无算术 DSL，经 OperandFunc 表达），再
	// 昨柱 == 近 Lookback 日窗口最大值（不含今日、左端越界钳 0，Eq 恰等
	// 等价旧全等比较）。历史引用与窗口逐日对截断前缀求值，MACDHist 因果
	// 递推与旧整段 Series 对应日逐位一致，旧 len(hist)!=len(dks) 防御天然
	// 成立。旧组件无 slow+signal 预检（与买侧 MACD反转 不同，台账附录 B），
	// n<2 由"昨柱无效值不触发"兜住，不引入 MinKlines 改变预热行为。
	hist := rule.MACDHist(s.Fast, s.Slow, s.Signal)
	prev, err := rule.RefOperand(1, hist)
	if err != nil {
		return false
	}
	fall, err := rule.NewCompare(hist, rule.Lt, rule.OperandFunc("昨柱-MinDiff", func(in rule.Input) rule.Value {
		v := prev.Value(in)
		if !v.Valid() {
			return rule.InvalidValue()
		}
		return rule.FloatValue(v.Float() - s.MinDiff)
	}))
	if err != nil {
		return false
	}
	winMax, err := rule.WindowMax(s.Lookback, hist, false)
	if err != nil {
		return false
	}
	peak, err := rule.NewCompare(prev, rule.Eq, winMax)
	if err != nil {
		return false
	}
	and, err := rule.NewAnd(fall, peak)
	if err != nil {
		return false
	}
	return and.Eval(rule.NewInput(code, dks, nil, nil))
}

// MACD买入后连跌 是买入若干交易日后，MACD 柱子连续下跌的卖出策略。
// Fast 表示快线 EMA 周期，默认 12。
// Slow 表示慢线 EMA 周期，默认 26。
// Signal 表示 DEA EMA 周期，默认 9。
// AfterDays 表示买入后至少第几个交易日才开始判断，默认 1。
// Days 表示 MACD 柱子连续下跌天数，默认 1。
type MACD买入后连跌 struct {
	Fast      int
	Slow      int
	Signal    int
	AfterDays int
	Days      int
}

func (s MACD买入后连跌) Name() string {
	afterDays := s.AfterDays
	if afterDays == 0 {
		afterDays = 1
	}
	days := s.Days
	if days == 0 {
		days = 1
	}
	return fmt.Sprintf("买入%d天后MACD连跌%d天", afterDays, days)
}

func (s MACD买入后连跌) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	if s.Fast == 0 {
		s.Fast = 12
	}
	if s.Slow == 0 {
		s.Slow = 26
	}
	if s.Signal == 0 {
		s.Signal = 9
	}
	if s.AfterDays == 0 {
		s.AfterDays = 1
	}
	if s.Days == 0 {
		s.Days = 1
	}

	n := len(dks)
	if n < s.Days+1 {
		return false
	}

	buyIdx := -1
	buyDate := buy.Time.Format("2006-01-02")
	for i := n - 1; i >= 0; i-- {
		if dks[i].Time.Format("2006-01-02") == buyDate {
			buyIdx = i
			break
		}
	}
	if buyIdx < 0 || n-1-buyIdx < s.AfterDays {
		return false
	}

	// 判断委托共用内核：MACD 柱序列用内核 MACDHist（因果 EMA 递推，与旧
	// 整段 Series 对应日逐位一致，旧 len(hist)!=n 防御天然成立）；连跌判定
	// 等旧 FallingStreak(hist) >= Days——末尾连续严格递减步数与
	// Consecutive(Days, 0, 今柱<昨柱, 含今日) 的连续满足根数逐位一致
	// （第 0 根昨柱无效不满足，步数封顶 n-1，同旧循环 i>0 终止）。旧的
	// n<Days+1、逆序日期串定位买入日、n-1-buyIdx<AfterDays 预检查原样
	// 保留在门面，保持判定次序。
	hist := rule.MACDHist(s.Fast, s.Slow, s.Signal)
	prev, err := rule.RefOperand(1, hist)
	if err != nil {
		return false
	}
	fall, err := rule.NewCompare(hist, rule.Lt, prev)
	if err != nil {
		return false
	}
	consec, err := rule.NewConsecutive(s.Days, 0, fall, true)
	if err != nil {
		return false
	}
	return consec.Eval(rule.NewInput(code, dks, nil, &buy))
}
