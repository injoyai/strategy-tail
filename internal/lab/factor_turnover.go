package lab

// factor_turnover.go：分组成员换手纯函数（Task 3 Step 3）。
// turnover(t, p) = 1 - |G(t) ∩ G(t-p)| / |G(t)|，分母为当前组规模；
// 两期组规模如实披露，并列块导致规模变化时不截断成员制造固定组数。

// TurnoverPoint 单期换手点。前期组缺失（nil 或空集）时 Turnover 与
// PriorSize 为 null，不以 0 冒充真实换手。
type TurnoverPoint struct {
	Date        string   `json:"date"`
	Turnover    *float64 `json:"turnover"`
	PriorSize   *int     `json:"priorSize"`
	CurrentSize int      `json:"currentSize"`
}

// groupTurnoverSeries 计算逐期换手序列：从下标 period 起，G(t)=members[i]、
// G(t-p)=members[i-period]。当前组缺失的日期无该期分组数据，直接跳过；
// 前期组缺失但当前组存在时，Turnover 与 PriorSize 为 null、CurrentSize
// 如实记录。period<1 或 dates/members 长度不一致为合同违反，返回 nil。
func groupTurnoverSeries(dates []string, members []map[string]struct{}, period int) []TurnoverPoint {
	if period < 1 || len(dates) != len(members) {
		return nil
	}
	points := make([]TurnoverPoint, 0, len(dates))
	for i := period; i < len(dates); i++ {
		cur := members[i]
		if len(cur) == 0 {
			continue
		}
		p := TurnoverPoint{Date: dates[i], CurrentSize: len(cur)}
		prior := members[i-period]
		if len(prior) == 0 {
			points = append(points, p)
			continue
		}
		inter := 0
		for m := range cur {
			if _, ok := prior[m]; ok {
				inter++
			}
		}
		tov := 1 - float64(inter)/float64(len(cur))
		ps := len(prior)
		p.Turnover, p.PriorSize = &tov, &ps
		points = append(points, p)
	}
	return points
}
