package strategies

import (
	"fmt"
	"time"

	"github.com/injoyai/conv"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/tdx/extend"
)

// SellAt 是固定时间卖出策略。
// After 表示买入后第几个交易日卖出，0 表示下一次进入卖出判断时对应的第一天。
// Time 表示分钟线卖出时间，默认 10:00:00。
// 如果指定日期存在分钟线，则选择当天第一根时间大于等于 Time 的分钟 K 线收盘价卖出。
// 如果没有分钟线，则退化为 future[After] 的日线开盘价卖出。
type SellAt struct {
	After int
	Time  string
}

func (s SellAt) Name() string {
	return conv.String(s.After+1) + "天后卖出"
}

func (s SellAt) Sell(code string, history, future extend.Klines, getMinklines func(after int) core.Klines, buy core.Buy) *core.Sell {
	if len(future) <= s.After {
		return nil
	}

	sellTime := s.Time
	if len(sellTime) == 0 {
		sellTime = "10:00:00"
	}

	for _, v := range getMinklines(s.After) {
		if v.Time.Format(time.TimeOnly) >= sellTime {
			return &core.Sell{
				Code:  code,
				Time:  v.Time,
				Price: v.Close,
			}
		}
	}

	return &core.Sell{
		Code:  code,
		Time:  future[s.After].Time,
		Price: future[s.After].Open,
	}
}

// SellTPSL 是止盈/止损卖出策略。
// TakeProfit 表示止盈比例，例如 0.10 表示盈利达到 10% 触发止盈。
// StopLoss 表示止损比例，例如 0.05 表示亏损达到 5% 触发止损。
// 策略会逐日遍历 future，用每日收盘价相对买入价计算收益率。
// 触发后优先使用下一交易日收盘价卖出；如果已经没有下一交易日，则使用触发当日收盘价卖出。
type SellTPSL struct {
	TakeProfit float64
	StopLoss   float64
}

func (s SellTPSL) Name() string {
	switch {
	case s.TakeProfit > 0 && s.StopLoss > 0:
		return fmt.Sprintf("止盈%.2f%%/止损%.2f%%", s.TakeProfit*100, s.StopLoss*100)
	case s.TakeProfit > 0:
		return fmt.Sprintf("止盈%.2f%%", s.TakeProfit*100)
	case s.StopLoss > 0:
		return fmt.Sprintf("止损%.2f%%", s.StopLoss*100)
	default:
		return "止盈止损"
	}
}

func (s SellTPSL) Sell(code string, history, future extend.Klines, getMinklines func(after int) core.Klines, buy core.Buy) *core.Sell {
	if len(future) == 0 {
		return nil
	}
	if s.TakeProfit <= 0 && s.StopLoss <= 0 {
		return nil
	}
	buyPrice := buy.Price.Float64()
	if buyPrice <= 0 {
		return nil
	}

	for i := 0; i < len(future); i++ {
		closePrice := future[i].Close.Float64()
		rate := (closePrice - buyPrice) / buyPrice

		trigger := false
		if s.TakeProfit > 0 && rate >= s.TakeProfit {
			trigger = true
		}
		if s.StopLoss > 0 && rate <= -s.StopLoss {
			trigger = true
		}
		if !trigger {
			continue
		}

		if i+1 < len(future) {
			return &core.Sell{
				Code:  code,
				Time:  future[i+1].Time,
				Price: future[i+1].Close,
			}
		}

		return &core.Sell{
			Code:  code,
			Time:  future[i].Time,
			Price: future[i].Close,
		}
	}

	return nil
}
