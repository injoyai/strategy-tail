package factor

import (
	"fmt"
	"math"

	"github.com/injoyai/strategy-tail/lib/extend"
)

const defaultTurnoverPerPriceMoveDays = 1

// 每百分点成交额 是含当日的近 N 日总成交额（亿元）除以各日绝对收盘涨跌幅之和（百分点）。
// 值越大，表示每 1 个百分点价格变动对应的历史成交额越高。默认 N=1，至少需要 N+1 根 K 线。
// 有效平盘日的成交额计入总成交额。必要行情无效、历史不足或窗口全平盘时返回 NaN。
type 每百分点成交额 struct {
	Days int
}

func (f 每百分点成交额) Name() string {
	return fmt.Sprintf("每1%%涨跌成交额(%d)", daysOr(f.Days, defaultTurnoverPerPriceMoveDays))
}

func (f 每百分点成交额) Value(_ string, dks extend.Klines) float64 {
	days := daysOr(f.Days, defaultTurnoverPerPriceMoveDays)
	if len(dks) <= days {
		return math.NaN()
	}
	window := dks[len(dks)-days-1:]
	var turnover, movePct float64
	for i := 1; i < len(window); i++ {
		previous, current := window[i-1], window[i]
		if previous == nil || previous.Kline == nil || current == nil || current.Kline == nil {
			return math.NaN()
		}
		if !(previous.Close > 0 && previous.Close <= math.MaxFloat64 &&
			current.Close > 0 && current.Close <= math.MaxFloat64 &&
			current.Amount > 0 && current.Amount <= math.MaxFloat64) {
			return math.NaN()
		}
		turnover += current.Amount / amountScaleYuan
		movePct += math.Abs((current.Close-previous.Close)/previous.Close) * 100
	}
	if movePct == 0 || math.IsInf(movePct, 0) {
		return math.NaN()
	}
	value := turnover / movePct
	if math.IsInf(value, 0) {
		return math.NaN()
	}
	return value
}
