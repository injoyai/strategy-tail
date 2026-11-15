package lab

import (
	"fmt"
	"math"
	"sort"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/portfolioresearch"
	"github.com/injoyai/strategy-tail/lib/extend"
	factor "github.com/injoyai/strategy-tail/strategies/factor"
)

// FactorScoreSpec describes one factor's ranking direction in an account backtest.
type FactorScoreSpec struct {
	Kind          string `json:"kind"`
	Days          int    `json:"days"`
	FactorVersion int    `json:"factorVersion,omitempty"`
	Direction     string `json:"direction"`
}

type accountScoreFactor struct {
	factor core.Factor
	lower  bool
}

func (s StrategySpec) buildScoreFactors() ([]accountScoreFactor, error) {
	if len(s.FactorScores) == 0 {
		return nil, nil
	}
	if s.Run.Account == nil {
		return nil, fmt.Errorf("因子评分需要账户回测")
	}
	factors := make([]accountScoreFactor, 0, len(s.FactorScores))
	seen := make(map[struct {
		kind string
		days int
	}]bool, len(s.FactorScores))
	for i, rule := range s.FactorScores {
		entry, ok := factor.Catalog(rule.Kind)
		if !ok || rule.Days <= 0 {
			return nil, fmt.Errorf("第 %d 项因子评分的因子类型或回看天数无效", i+1)
		}
		if rule.FactorVersion != 0 && rule.FactorVersion != entry.ImplementationVersion {
			return nil, fmt.Errorf("第 %d 项因子评分的实现版本不匹配：当前版本 %d，请求 %d", i+1, entry.ImplementationVersion, rule.FactorVersion)
		}
		if rule.Direction != portfolioresearch.DirectionHigherIsBetter && rule.Direction != portfolioresearch.DirectionLowerIsBetter {
			return nil, fmt.Errorf("第 %d 项因子评分需要有效的评分方向", i+1)
		}
		key := struct {
			kind string
			days int
		}{rule.Kind, rule.Days}
		if seen[key] {
			return nil, fmt.Errorf("第 %d 项因子评分与前项重复：%s(%d)", i+1, rule.Kind, rule.Days)
		}
		seen[key] = true
		factors = append(factors, accountScoreFactor{
			factor: factor.BuildWithData(rule.Kind, rule.Days, common.ResearchData),
			lower:  rule.Direction == portfolioresearch.DirectionLowerIsBetter,
		})
	}
	return factors, nil
}

type scoredAccountBuyer struct {
	core.Buyer
	factors []accountScoreFactor
}

// prioritize ranks the complete cases from one signal-day candidate set.
// Every factor ranks the same stocks, so the sum of average ranks orders them
// identically to the mean of percentile scores. Stable sorting keeps code order
// for ties. The next session consumes this frozen order for open fills.
func (b scoredAccountBuyer) prioritize(entries []accountBuySignal, prefixes map[string]extend.Klines) []accountBuySignal {
	candidates := entries[:0]
	columns := make([][]float64, len(b.factors))
	values := make([]float64, len(b.factors))
	for _, entry := range entries {
		valid := true
		for i, f := range b.factors {
			value := f.factor.Value(entry.code, prefixes[entry.code])
			if math.IsNaN(value) || math.IsInf(value, 0) {
				valid = false
				break
			}
			if f.lower {
				value = -value
			}
			values[i] = value
		}
		if !valid {
			continue
		}
		for i, value := range values {
			columns[i] = append(columns[i], value)
		}
		candidates = append(candidates, entry)
	}
	for _, column := range columns {
		for i, rank := range avgRanks(column) {
			candidates[i].priority += rank
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].priority > candidates[j].priority
	})
	return candidates
}
