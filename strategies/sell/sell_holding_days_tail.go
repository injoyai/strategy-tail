package sell

import (
	"fmt"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/rule"
)

// A持仓N天尾盘 买入后满 N 个交易日，在当日尾盘时段卖出。
// Days 表示持仓天数（不含买入日），默认 1（即次日尾盘卖出）。
// Time 表示尾盘起始时间（含），默认 "14:55:00"（本地 5 分钟线最后一根，
// 其收盘价 ≈ 当日收盘价，按该根快照成交即接近收盘卖出）。
//
// 引擎在分钟级循环中逐根覆写 dks 最后一根的时间戳后调用本组件：
// 当日时间未到 Time 时不触发，到 Time 后触发，成交价为该分钟快照收盘。
// 当日无分钟数据时（引擎退化为单根日 K，快照时间 00:00:00），
// 成交价即当日收盘，视为尾盘卖出，同样触发。
type A持仓N天尾盘 struct {
	Days int
	Time string
}

func (s A持仓N天尾盘) Name() string {
	days := s.Days
	if days == 0 {
		days = 1
	}
	t := s.Time
	if t == "" {
		t = "14:55:00"
	}
	return fmt.Sprintf("%d天尾盘(%s)卖出", days, t)
}

func (s A持仓N天尾盘) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	days := s.Days
	if days == 0 {
		days = 1
	}
	sellTime := s.Time
	if sellTime == "" {
		sellTime = "14:55:00"
	}
	// 判断委托共用内核 And 组合：已持仓交易日数 >= days（与 A持仓N天
	// 同口径，内核 PositionHoldingDays 固化顺序定位），且尾盘时刻已到。
	// 尾盘时刻经规则级逃逸口表达：末根快照时间 00:00:00 视为退化日线
	// 收盘必触发，否则按字符串比较 tod >= sellTime（与旧一致）；
	// And 短路先判持仓天数，求值顺序与旧预检查次序一致。
	tail, err := rule.NewRuleFunc("尾盘时刻已到", func(in rule.Input) bool {
		n := len(in.Klines)
		if n == 0 {
			return false
		}
		tod := in.Klines[n-1].Time.Format(time.TimeOnly)
		if tod == "00:00:00" {
			return true
		}
		return tod >= sellTime
	})
	if err != nil {
		return false
	}
	holding, err := rule.NewCompare(rule.PositionHoldingDays(), rule.Ge, rule.Literal(rule.FloatValue(float64(days))))
	if err != nil {
		return false
	}
	r, err := rule.NewAnd(holding, tail)
	if err != nil {
		return false
	}
	return r.Eval(rule.NewInput(code, dks, nil, &buy))
}
