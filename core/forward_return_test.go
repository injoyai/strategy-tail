package core_test

import (
	"testing"

	"github.com/injoyai/strategy-tail/core"
)

func TestDefaultForwardDays(t *testing.T) {
	days := core.DefaultForwardDays()
	expected := []int{1, 3, 5, 10, 15, 20, 30}
	if len(days) != len(expected) {
		t.Fatalf("expected %d days, got %d", len(expected), len(days))
	}
	for i, d := range days {
		if d != expected[i] {
			t.Fatalf("days[%d]: expected %d, got %d", i, expected[i], d)
		}
	}
}
