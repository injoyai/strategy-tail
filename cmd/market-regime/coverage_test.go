package main

import (
	"testing"

	"github.com/injoyai/strategy-tail/internal/researchrun"
)

func TestCoverageTotals(t *testing.T) {
	r := &AnalysisResult{Coverage: []YearCoverage{
		{Year: 2025, Coverage: researchrun.Coverage{Requested: 10, Completed: 8, Skipped: 2}},
		{Year: 2026, Coverage: researchrun.Coverage{Requested: 12, Completed: 11, Skipped: 1}},
	}}

	requested, completed, skipped := coverageTotals(r)
	if requested != 22 || completed != 19 || skipped != 3 {
		t.Fatalf("coverageTotals() = (%d, %d, %d), want (22, 19, 3)", requested, completed, skipped)
	}
}
