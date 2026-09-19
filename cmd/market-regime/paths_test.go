package main

import (
	"path/filepath"
	"testing"

	common "github.com/injoyai/strategy-tail"
)

func TestReportOutputDirUsesProjectRoot(t *testing.T) {
	want := filepath.Join(common.RuntimeRoot(), "output", "market-regime")
	if got := reportOutputDir(); got != want {
		t.Fatalf("reportOutputDir() = %q, want %q", got, want)
	}
}
