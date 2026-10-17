package common

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/injoyai/conv/cfg"
)

func TestConfigUsesRuntimeRoot(t *testing.T) {
	if os.Getenv("STRATEGY_TAIL_CONFIG_TEST") == "1" {
		if got := cfg.GetInt("screen.port"); got != 9191 {
			t.Fatalf("screen.port = %d, want root config value 9191", got)
		}
		cost, pos, years, benchmark, iterations := LoadBacktestConfig()
		if cost.CommissionRate != 0.002 || pos.MaxPerCode != 3 || len(years) != 1 || years[0] != 2025 || benchmark != "sh000001" || iterations != 42 {
			t.Fatalf("root backtest config not loaded: %v %v %v %s %d", cost, pos, years, benchmark, iterations)
		}
		if Pull != nil || Manage != nil || DefaultUniverse != nil {
			t.Fatal("loading config must not initialize runtime resources")
		}
		return
	}

	root := t.TempDir()
	for _, dir := range []string{"config", "cmd/screen/config"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	config := "screen:\n  port: 9191\nbacktest:\n  cost:\n    commission_rate: 0.002\n  position:\n    max_per_code: 3\n  years: [2025]\n  benchmark: sh000001\n  monte_carlo_iterations: 42\n"
	if err := os.WriteFile(filepath.Join(root, "config/config.yaml"), []byte(config), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cmd/screen/config/config.yaml"), []byte("screen:\n  port: 8080\n"), 0644); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{root, filepath.Join(root, "cmd/screen")} {
		cmd := exec.Command(executable, "-test.run=^TestConfigUsesRuntimeRoot$")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "STRATEGY_TAIL_ROOT="+root, "STRATEGY_TAIL_CONFIG_TEST=1", "screen.port=7070")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("config from %s: %v\n%s", dir, err, output)
		}
	}
}
