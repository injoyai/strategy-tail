package common

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/injoyai/conv/cfg"
)

func TestConfigUsesRuntimeRoot(t *testing.T) {
	if os.Getenv("STRATEGY_TAIL_CONFIG_TEST") == "1" {
		if got := cfg.GetInt("screen.port"); got != 9191 {
			t.Fatalf("screen.port = %d, want root config value 9191", got)
		}
		cost, pos := LoadBacktestConfig()
		if cost.CommissionRate != 0.002 || pos.MaxPerCode != 3 {
			t.Fatalf("root backtest config not loaded: %v %v", cost, pos)
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
	config := "screen:\n  port: 9191\nbacktest:\n  cost:\n    commission_rate: 0.002\n  position:\n    max_per_code: 3\n"
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

func TestMySQLConfigPrecedence(t *testing.T) {
	if want := os.Getenv("STRATEGY_TAIL_MYSQL_CONFIG_TEST"); want != "" {
		got, err := MySQLDSN()
		if want == "missing" {
			if err == nil {
				t.Fatal("missing MySQL configuration silently accepted")
			}
		} else if err != nil || got != want {
			t.Fatalf("MySQL config precedence failed; got error=%v", err)
		}
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, private, env, want string
		explicit                 bool
	}{
		{"private file", "private-test", "", "private-test", false},
		{"environment overrides empty public file", "", "env-test", "env-test", true},
		{"environment overrides private file", "private-test", "env-test", "env-test", true},
		{"explicit empty environment", "private-test", "", "missing", true},
		{"missing", "", "", "missing", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "config"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "config/config.yaml"), []byte("pull:\n  mysql: \"\"\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if tc.private != "" {
				if err := os.WriteFile(filepath.Join(root, "config/mysql.yaml"), []byte("pull:\n  mysql: "+tc.private+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(executable, "-test.run=^TestMySQLConfigPrecedence$")
			for _, value := range os.Environ() {
				if !strings.HasPrefix(value, "pull.mysql=") {
					cmd.Env = append(cmd.Env, value)
				}
			}
			cmd.Env = append(cmd.Env, "STRATEGY_TAIL_ROOT="+root, "STRATEGY_TAIL_MYSQL_CONFIG_TEST="+tc.want)
			if tc.explicit {
				cmd.Env = append(cmd.Env, "pull.mysql="+tc.env)
			}
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("config subprocess failed: %v\n%s", err, output)
			}
		})
	}
}
