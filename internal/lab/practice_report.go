package lab

// 单题报告只由冻结会话与行情派生，不写回会话、不包含结束后的复盘行情。
// 交易收益率沿用 evaluate；账户回报以整题预算为分母，包含未使用现金。
type practiceReport struct {
	Traded         bool                  `json:"traded"`
	Status         string                `json:"status"`
	StartDate      string                `json:"startDate"`
	EndDate        string                `json:"endDate"`
	Budget         float64               `json:"budget"`
	BuyCost        float64               `json:"buyCost"`
	SellIncome     float64               `json:"sellIncome"`
	Fees           float64               `json:"fees"`
	Cash           float64               `json:"cash"`
	PositionValue  float64               `json:"positionValue"`
	Equity         float64               `json:"equity"`
	AccountPct     *float64              `json:"accountPct"`
	WaitDays       *int                  `json:"waitDays"`
	MaxDrawdown    *float64              `json:"maxDrawdown"`
	MaxDrawdownPct *float64              `json:"maxDrawdownPct"`
	Points         []practiceReportPoint `json:"points"`
}

type practiceReportPoint struct {
	Day    int     `json:"day"`
	Date   string  `json:"date"`
	Equity float64 `json:"equity"`
	PnL    float64 `json:"pnl"`
	Pct    float64 `json:"pct"`
}

func practiceReportOf(s *practiceSession, m *practiceMarket, trades []practiceTradeView) *practiceReport {
	if s.Phase != practicePhaseFinished || m == nil {
		return nil
	}
	r := &practiceReport{Status: "untraded", StartDate: m.Days[s.SignalIndex].Date,
		EndDate: m.Days[s.CurrentIndex].Date, Budget: s.BudgetYuan, Cash: s.BudgetYuan, Equity: s.BudgetYuan,
		Traded: s.BuyIndex >= 0 && s.Quantity > 0}
	if !r.Traded {
		return r
	}
	r.Status = "open"
	if s.Stage == practiceStageSold {
		r.Status = "sold"
	}
	r.BuyCost = s.BuyCostYuan
	r.SellIncome = s.SellIncomeYuan
	wait := s.BuyIndex - s.SignalIndex
	r.WaitDays = &wait
	for _, t := range trades {
		r.Fees += t.Fee
	}
	peak := s.BudgetYuan
	drawdown, drawdownPct := 0.0, 0.0
	for i := s.SignalIndex; i <= s.CurrentIndex; i++ {
		cash, value := s.BudgetYuan, 0.0
		if i >= s.BuyIndex {
			cash -= s.BuyCostYuan
			if s.SellIndex >= 0 && i >= s.SellIndex {
				cash += s.SellIncomeYuan
			} else {
				value = practicePriceYuan(m.Days[i].Close) * float64(s.Quantity)
			}
		}
		equity := cash + value
		if equity > peak {
			peak = equity
		}
		if peak-equity > drawdown {
			drawdown = peak - equity
		}
		if peak > 0 && (peak-equity)/peak*100 > drawdownPct {
			drawdownPct = (peak - equity) / peak * 100
		}
		pnl := equity - s.BudgetYuan
		pct := 0.0
		if s.BudgetYuan > 0 {
			pct = pnl / s.BudgetYuan * 100
		}
		r.Points = append(r.Points, practiceReportPoint{Day: i - s.SignalIndex, Date: m.Days[i].Date, Equity: equity, PnL: pnl, Pct: pct})
		r.Cash, r.PositionValue, r.Equity = cash, value, equity
	}
	if s.BudgetYuan > 0 {
		r.AccountPct = yuanPtr((r.Equity - s.BudgetYuan) / s.BudgetYuan * 100)
	}
	r.MaxDrawdown, r.MaxDrawdownPct = &drawdown, &drawdownPct
	return r
}
