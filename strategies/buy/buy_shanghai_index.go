package buy

import (
	"fmt"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

// A上证N日均线向上 是以上证指数5日均线方向作为市场过滤条件的买入策略。
// 说明：当前 Buy 接口只传入个股K线，因此该策略会在内部自行拉取上证指数日线。
// 当上证指数最近 Lookback 步的 5 日均线持续向上时返回 true。
// 可单独使用，也可与其他个股买点通过 buy.And 组合使用。
type A上证N日均线向上 struct {
	Ks       protocol.Klines
	Period   int
	Lookback int
	MinSlope float64
}

func (b A上证N日均线向上) Name() string {
	period := b.Period
	if period == 0 {
		period = 5
	}
	return fmt.Sprintf("上证%d日均线向上", period)
}

func (b A上证N日均线向上) Buy(code string, dks extend.Klines) bool {
	period := b.Period
	if period == 0 {
		period = 5
	}
	lookback := b.Lookback
	if lookback == 0 {
		lookback = 1
	}
	if len(dks) == 0 || len(b.Ks) == 0 {
		return false
	}

	t := dks[len(dks)-1].Time
	aligned := make(protocol.Klines, 0, len(b.Ks))
	for _, k := range b.Ks {
		if k == nil {
			continue
		}
		if !k.Time.After(t) {
			aligned = append(aligned, k)
		}
	}
	if len(aligned) == 0 {
		return false
	}

	return maUp2(aligned, period, lookback, b.MinSlope)
}
