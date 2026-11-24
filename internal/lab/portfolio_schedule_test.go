package lab

import (
	"math"
	"testing"

	"github.com/injoyai/strategy-tail/internal/portfolioresearch"
	"github.com/injoyai/strategy-tail/researchdata"
)

func TestPortfolioRebalanceDays(t *testing.T) {
	dates := []string{"2025-01-29", "2025-01-30", "2025-02-05", "2025-02-06", "2025-02-10"}
	for _, tc := range []struct {
		frequency string
		want      []string
	}{
		{"daily", dates}, {"weekly", []string{dates[0], dates[2], dates[4]}}, {"monthly", []string{dates[0], dates[2]}},
	} {
		t.Run(tc.frequency, func(t *testing.T) {
			got, err := portfolioRebalanceDays(tc.frequency, dates)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("days=%v want %v", got, tc.want)
			}
			for _, date := range tc.want {
				if !got[date] {
					t.Fatalf("missing rebalance day %s", date)
				}
			}
		})
	}
	for _, dates := range [][]string{{"invalid"}, {"2025-01-01", "invalid"}} {
		if _, err := portfolioRebalanceDays("weekly", dates); err == nil {
			t.Fatal("invalid date accepted")
		}
	}
	// ISO week years differ from calendar years around New Year.
	days, err := portfolioRebalanceDays("weekly", []string{"2024-12-30", "2025-01-02", "2025-01-06"})
	if err != nil || len(days) != 2 || days["2025-01-02"] {
		t.Fatalf("ISO week days=%v err=%v", days, err)
	}
}

func TestPortfolioWeeklyMonthlyHoldBetweenRebalances(t *testing.T) {
	for _, frequency := range []string{"daily", "weekly", "monthly"} {
		for _, carry := range []bool{false, true} {
			t.Run(frequency+map[bool]string{false: "/expire", true: "/carry"}[carry], func(t *testing.T) {
				d := benchPortfolioData(1, 2, 5) // 2024-01-01 through Friday, all in the same week/month.
				m := auditGoldenModel()
				m.Execution.Rebalance, m.Execution.CarryUnfilled = frequency, carry
				m.Execution.Cost = portfolioresearch.CostSpec{}
				m.PortfolioPolicy = portfolioresearch.PortfolioPolicy{Selection: portfolioresearch.PortfolioSelectionTopN, TopN: 1, CashBuffer: 0.1}
				scores := map[string]map[string]float64{}
				for i, date := range d.dates {
					scores[date] = map[string]float64{d.codes[i%2]: 2, d.codes[(i+1)%2]: 1}
				}
				ledgers, _, err := runPortfolioDays(m, scores, d, d.dates, 1000000, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				for i, l := range ledgers {
					if !l.Reconciled {
						t.Fatalf("unreconciled day %s", l.Date)
					}
					if frequency != "daily" && i > 1 && len(l.Fills) != 0 {
						t.Fatalf("non-rebalance day %s fills=%v", l.Date, l.Fills)
					}
				}
				if len(ledgers[1].Fills) != 1 {
					t.Fatal("first signal was not executed at next open")
				}
				if frequency == "daily" && len(ledgers[2].Fills) != 2 {
					t.Fatal("daily ranking change did not rebalance")
				}
			})
		}
	}
}

func TestPortfolioMissingMedianRetainsEligibleStocks(t *testing.T) {
	d := benchPortfolioData(1, 3, 1)
	m := auditGoldenModel()
	m.ValidatedFactors = m.ValidatedFactors[:1]
	m.TransformPipeline.Missing = portfolioresearch.TransformMissingCrossSectionMedian
	key, date := factorKey(m.ValidatedFactors[0]), d.dates[0]
	d.values = map[string]map[string]map[string]float64{key: {date: {d.codes[0]: 1, d.codes[1]: 3}}}
	r := &Runner{}
	transformed, err := r.transformFactors(m, d, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := transformed[key][date]
	if len(rows) != 3 || !rows[2].Filled || rows[2].FilledWith != 2 || math.Abs(rows[2].Final) > 1e-9 {
		t.Fatalf("median transform rows=%+v", rows)
	}
	scores, err := combinePortfolio(buildFactorSeries(m, transformed), m.TransformPipeline.Missing, nil, false)
	if err != nil || len(scores[date]) != 3 {
		t.Fatalf("scores=%v err=%v", scores, err)
	}
	// Membership and missing trading sessions remain excluded from median filling.
	r.universe = researchdata.NewStaticUniverse(researchdata.StaticUniverseConfig{Codes: func() []string { return d.codes[:2] }})
	for _, useUniverse := range []bool{true, false} {
		if !useUniverse {
			r.universe = nil
			delete(d.close[date], d.codes[2])
		}
		transformed, err = r.transformFactors(m, d, nil)
		if err != nil {
			t.Fatal(err)
		}
		scores, err = combinePortfolio(buildFactorSeries(m, transformed), m.TransformPipeline.Missing, nil, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := scores[date][d.codes[2]]; ok {
			t.Fatal("masked stock was restored by median filling")
		}
	}
}

func TestPortfolioNonRebalanceDayHonorsCarryUnfilled(t *testing.T) {
	for _, carry := range []bool{false, true} {
		t.Run(map[bool]string{false: "expire", true: "carry"}[carry], func(t *testing.T) {
			d := benchPortfolioData(1, 1, 4)
			code := d.codes[0]
			for _, date := range d.dates {
				d.close[date][code] = 10
				d.open[date][code] = 10
			}
			d.open[d.dates[1]][code], d.close[d.dates[1]][code] = 21, 21
			m := auditGoldenModel()
			m.Execution.Rebalance, m.Execution.CarryUnfilled = "weekly", carry
			m.Execution.Cost = portfolioresearch.CostSpec{}
			m.PortfolioPolicy = portfolioresearch.PortfolioPolicy{Selection: portfolioresearch.PortfolioSelectionTopN, TopN: 1, CashBuffer: 0.1}
			scores := map[string]map[string]float64{}
			for _, date := range d.dates {
				scores[date] = map[string]float64{code: 1}
			}
			ledgers, _, err := runPortfolioDays(m, scores, d, d.dates, 10000, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(ledgers[1].Fills) != 1 || !ledgers[1].Fills[0].Partial {
				t.Fatal("fixture must partially fill the initial buy")
			}
			want := 0
			if carry {
				want = 1
			}
			if len(ledgers[2].Fills) != want {
				t.Fatalf("carry=%v next day fills=%v", carry, ledgers[2].Fills)
			}
		})
	}
}
