package lab

import "github.com/injoyai/strategy-tail/core"

// Frozen when an attempt ends; extra review bars never enter these numbers.
type trainingQuestionResult struct {
	ID            string   `json:"id"`
	Status        string   `json:"status"`
	Code          string   `json:"code"`
	SignalDate    string   `json:"signalDate"`
	EndDate       string   `json:"endDate"`
	BuyCost       float64  `json:"buyCost"`
	SellIncome    float64  `json:"sellIncome"`
	Fees          float64  `json:"fees"`
	RealizedPnL   float64  `json:"realizedPnl"`
	ReturnPct     *float64 `json:"returnPct"`
	PositionValue float64  `json:"positionValue"`
	OpenPnL       float64  `json:"openPnl"`
	HoldingDays   int      `json:"holdingDays"`
}

func trainingResultOf(s *practiceSession, m *practiceMarket) trainingQuestionResult {
	v := practiceReviewViewOf(s, m)
	r := v.Report
	e := s.evaluate(m)
	row := trainingQuestionResult{ID: s.ID, Status: r.Status, Code: m.Code, SignalDate: m.Days[s.SignalIndex].Date, EndDate: r.EndDate, BuyCost: s.BuyCostYuan, SellIncome: s.SellIncomeYuan, Fees: r.Fees, PositionValue: r.PositionValue, HoldingDays: e.HoldingDays}
	if r.Status == "sold" {
		row.RealizedPnL = e.RealizedPnL
		row.ReturnPct = yuanPtr(e.RealizedPct)
	}
	if r.Status == "open" {
		row.OpenPnL = e.OpenPnL
		row.ReturnPct = yuanPtr(e.OpenPct)
	}
	return row
}

type trainingReport struct {
	ID              string                   `json:"id"`
	BankID          string                   `json:"bankId"`
	StrategyName    string                   `json:"strategyName"`
	StrategyVersion int                      `json:"strategyVersion"`
	FromYear        int                      `json:"fromYear"`
	ToYear          int                      `json:"toYear"`
	StartedAt       string                   `json:"startedAt"`
	EndedAt         string                   `json:"endedAt"`
	BankTotal       int                      `json:"bankTotal"`
	Practiced       int                      `json:"practiced"`
	Closed          int                      `json:"closed"`
	Untraded        int                      `json:"untraded"`
	Open            int                      `json:"open"`
	Wins            int                      `json:"wins"`
	Losses          int                      `json:"losses"`
	Breakeven       int                      `json:"breakeven"`
	WinRate         *float64                 `json:"winRate"`
	AverageReturn   *float64                 `json:"averageReturn"`
	RealizedPnL     float64                  `json:"realizedPnl"`
	PositionValue   float64                  `json:"positionValue"`
	OpenPnL         float64                  `json:"openPnl"`
	Fees            float64                  `json:"fees"`
	Coverage        *practiceScanCoverage    `json:"coverage"`
	Questions       []trainingQuestionResult `json:"questions"`
}

func trainingReportOf(t *practiceTraining, b *practiceBank) (*trainingReport, error) {
	if t.Phase != "finished" {
		return nil, errPracticeNotFinished
	}
	r := &trainingReport{ID: t.ID, BankID: b.ID, StrategyName: b.Spec.Name, StrategyVersion: b.Request.StrategyVersion, FromYear: b.Request.FromYear, ToYear: b.Request.ToYear, StartedAt: t.StartedAt, EndedAt: t.EndedAt, BankTotal: b.Total, Practiced: len(t.Attempts), Coverage: b.Coverage, Questions: []trainingQuestionResult{}}
	closed := []core.Trade{}
	for _, a := range t.Attempts {
		if a.Session.Phase != practicePhaseFinished || a.Result == nil {
			return nil, errPracticeCorrupt
		}
		q := *a.Result
		r.Questions = append(r.Questions, q)
		r.Fees += q.Fees
		switch q.Status {
		case "sold":
			r.RealizedPnL += q.RealizedPnL
			closed = append(closed, core.Trade{BuyCost: q.BuyCost, SellIncome: q.SellIncome})
		case "open":
			r.Open++
			r.PositionValue += q.PositionValue
			r.OpenPnL += q.OpenPnL
		case "untraded":
			r.Untraded++
		default:
			return nil, errPracticeCorrupt
		}
	}
	stats := core.Stats(closed)
	r.Closed = stats.Total
	r.Wins = stats.Win
	r.Losses = stats.Loss
	r.Breakeven = stats.Total - stats.Win - stats.Loss
	if stats.Total > 0 {
		r.WinRate = yuanPtr(stats.WinRate)
		r.AverageReturn = yuanPtr(stats.AvgProfit)
	}
	return r, nil
}
func (s *TrainingStore) Report(id string) (*trainingReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, e := s.training(id)
	if e != nil {
		return nil, e
	}
	b, e := s.bank(t.BankID)
	if e != nil {
		return nil, e
	}
	return trainingReportOf(t, b)
}
