package sell

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
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
	if len(dks) == 0 {
		return false
	}
	buyPrice := buy.Price.Float64()
	if buyPrice <= 0 {
		return false
	}

	// 定位买入日在 dks 中的索引
	buyIdx := -1
	buyDate := buy.Time.Format("2006-01-02")
	for i, k := range dks {
		if k.Time.Format("2006-01-02") == buyDate {
			buyIdx = i
			break
		}
	}
	if buyIdx < 0 {
		return false
	}

	// 扫描买入日（含）到今天的最高收盘价
	peak := 0.0
	for i := buyIdx; i < len(dks); i++ {
		c := dks[i].Close.Float64()
		if c > peak {
			peak = c
		}
	}
	if peak <= 0 {
		return false
	}

	current := dks[len(dks)-1].Close.Float64()
	drawdown := (peak - current) / peak
	return drawdown >= s.Drawdown
}
