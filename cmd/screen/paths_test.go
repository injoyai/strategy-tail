package main

import (
	"path/filepath"
	"testing"

	common "github.com/injoyai/strategy-tail"
)

func TestScreenRuntimePathsUseProjectRoot(t *testing.T) {
	t.Run("database", func(t *testing.T) {
		want := filepath.Join(common.RuntimeRoot(), "data", "database", "trade.db")
		if got := screenDBPath(); got != want {
			t.Fatalf("screenDBPath() = %q, want %q", got, want)
		}
	})

	t.Run("local web asset", func(t *testing.T) {
		want := filepath.Join(common.RuntimeRoot(), "cmd", "screen", "web", "app.js")
		if got := screenWebPath("app.js"); got != want {
			t.Fatalf("screenWebPath() = %q, want %q", got, want)
		}
	})
}
