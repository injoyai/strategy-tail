package buy

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// A因子TopN 按横截面因子排名买入：当日快照中名次 ∈ (0, N] 时买入。
// Asc=true 因子值越小名次越靠前（升序）；Asc=false 值越大越靠前（降序）。
//
// 契约：名次快照由回测任务启动前统一预填（internal/lab fillCrossSection），
// 本策略只读不写——单股视角看不到其他股票。无快照的日期恒 false
// （backtest_tail 直接跑本策略、或快照被 ClearCrossSection 清空后）。
type A因子TopN struct {
	Factor core.Factor
	N      int
	Asc    bool
}

func (b A因子TopN) Name() string {
	order := "降序"
	if b.Asc {
		order = "升序"
	}
	if b.Factor == nil {
		return "因子TopN(" + order + ")"
	}
	return fmt.Sprintf("%s TopN(%d,%s)", b.Factor.Name(), b.N, order)
}

func (b A因子TopN) Buy(code string, dks extend.Klines) bool {
	if b.Factor == nil || len(dks) == 0 || b.N <= 0 {
		return false
	}
	rank := core.CrossSectionRank(core.DayOf(dks[len(dks)-1].Time),
		core.TopNKey(b.Factor.Name(), b.Asc), code)
	return rank > 0 && rank <= b.N
}
