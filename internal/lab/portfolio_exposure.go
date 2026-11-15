package lab

import (
	"errors"
	"math"
	"strings"
	"time"

	"github.com/injoyai/strategy-tail/internal/portfolioresearch"
	"github.com/injoyai/strategy-tail/researchdata"
	"github.com/injoyai/tdx/v2/protocol"
)

const defaultPortfolioIndustryDataset = "industry.daily"

type portfolioExposure struct {
	industry string
	logSize  float64
}

type portfolioExposures struct {
	view                         researchdata.View
	industryDataset, sizeDataset string
	rows                         map[string]map[string]portfolioExposure
}

type PortfolioExposureCoverage struct {
	IndustryDataset string `json:"industryDataset"`
	SizeDataset     string `json:"sizeDataset,omitempty"`
	Requested       int    `json:"requested"`
	WithIndustry    int    `json:"withIndustry"`
	WithSize        int    `json:"withSize"`
}

func (p *portfolioExposures) coverage() *PortfolioExposureCoverage {
	if p == nil {
		return nil
	}
	out := &PortfolioExposureCoverage{IndustryDataset: p.industryDataset, SizeDataset: p.sizeDataset}
	for _, rows := range p.rows {
		out.Requested += len(rows)
		for _, row := range rows {
			if row.industry != "" {
				out.WithIndustry++
			}
			if !math.IsNaN(row.logSize) && !math.IsInf(row.logSize, 0) {
				out.WithSize++
			}
		}
	}
	return out
}

func newPortfolioExposures(view researchdata.View, neutralize portfolioresearch.NeutralizeSpec) *portfolioExposures {
	dataset := neutralize.IndustryDataset
	if dataset == "" {
		dataset = defaultPortfolioIndustryDataset
	}
	return &portfolioExposures{view: view, industryDataset: dataset, sizeDataset: neutralize.SizeDataset,
		rows: map[string]map[string]portfolioExposure{}}
}

func (p *portfolioExposures) read(at time.Time, codes []string) map[string]portfolioExposure {
	day := at.Format(time.DateOnly)
	rows := p.rows[day]
	if rows == nil {
		rows = make(map[string]portfolioExposure, len(codes))
		p.rows[day] = rows
	}
	for _, code := range codes {
		if _, ok := rows[code]; ok {
			continue
		}
		e := portfolioExposure{logSize: math.NaN()}
		if p.view != nil {
			if row, ok := p.view.Latest(p.industryDataset, code, at); ok {
				e.industry = strings.TrimSpace(row.Attributes["industry"])
			}
			if p.sizeDataset != "" {
				if row, ok := p.view.Latest(p.sizeDataset, code, at); ok {
					if v, exists := row.Values["log_float_cap"]; exists {
						e.logSize = v
					}
				}
			}
		}
		rows[code] = e
	}
	return rows
}

func (p *portfolioExposures) Exposures(date string, codes []string) ([]string, []float64, error) {
	if p == nil {
		return nil, nil, errors.New("缺少可读取的风险暴露数据源")
	}
	day, err := time.ParseInLocation(time.DateOnly, date, protocol.Location())
	if err != nil {
		return nil, nil, err
	}
	rows := p.read(accountCloseTime(day), codes)
	industries, sizes := make([]string, len(codes)), make([]float64, len(codes))
	for i, code := range codes {
		e := rows[code]
		industries[i], sizes[i] = e.industry, e.logSize
	}
	return industries, sizes, nil
}

func (p *portfolioExposures) industries(date string, codes []string) map[string]string {
	if p == nil {
		return nil
	}
	industry, _, err := p.Exposures(date, codes)
	if err != nil {
		return nil // date is a validated loaded trading session.
	}
	out := make(map[string]string, len(codes))
	for i, code := range codes {
		if industry[i] != "" {
			out[code] = industry[i]
		}
	}
	return out
}
