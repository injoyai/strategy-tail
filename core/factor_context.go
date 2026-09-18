package core

import (
	"math"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/researchdata"
)

// FactorContext is the point-in-time input for factors that need more than K
// lines. Data queries must use AsOf so future publications and revisions remain
// invisible to historical calculations.
type FactorContext struct {
	Code   string
	AsOf   time.Time
	Klines extend.Klines
	Data   researchdata.View
}

// ContextFactor supports financial, fundamental, announcement and alternative
// data while the original Factor remains source compatible for price factors.
// Missing inputs return math.NaN(), matching the existing Factor contract.
type ContextFactor interface {
	Name() string
	ValueAt(FactorContext) float64
}

type klineContextFactor struct {
	factor Factor
}

func (f klineContextFactor) Name() string { return f.factor.Name() }

func (f klineContextFactor) ValueAt(input FactorContext) float64 {
	return f.factor.Value(input.Code, input.Klines)
}

// Contextual adapts an existing K-line Factor to ContextFactor.
func Contextual(factor Factor) ContextFactor {
	if factor == nil {
		return nil
	}
	return klineContextFactor{factor: factor}
}

type boundContextFactor struct {
	factor ContextFactor
	data   researchdata.View
}

func (f boundContextFactor) Name() string { return f.factor.Name() }

func (f boundContextFactor) Value(code string, dks extend.Klines) float64 {
	if len(dks) == 0 {
		return math.NaN()
	}
	return f.factor.ValueAt(FactorContext{
		Code:   code,
		AsOf:   dks[len(dks)-1].Time,
		Klines: dks,
		Data:   f.data,
	})
}

// BindContextFactor adapts a context-aware factor back to the original Factor
// interface, allowing existing Buyer, TopN and backtest paths to use it without
// changing their public contracts.
func BindContextFactor(factor ContextFactor, data researchdata.View) Factor {
	if factor == nil {
		return nil
	}
	return boundContextFactor{factor: factor, data: data}
}
