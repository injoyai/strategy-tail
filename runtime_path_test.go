package common

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportDoesNotInitializeRuntime(t *testing.T) {
	if Pull != nil || Manage != nil || DefaultUniverse != nil {
		t.Fatal("导入 common 包不应初始化运行时依赖")
	}
}

func TestFindModuleRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/root\n"), 0644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "internal", "lab")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}

	got, ok := findModuleRoot(nested)
	if !ok {
		t.Fatal("未找到模块根目录")
	}
	want, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(want) {
		t.Fatalf("模块根目录不符: got %q want %q", got, want)
	}
}

func TestSelectRuntimeRoot(t *testing.T) {
	moduleRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(moduleRoot, "go.mod"), []byte("module example.test/root\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(moduleRoot, "internal", "lab")
	if err := os.MkdirAll(cwd, 0755); err != nil {
		t.Fatal(err)
	}

	t.Run("从当前目录向上发现模块", func(t *testing.T) {
		if got := selectRuntimeRoot(cwd, "", ""); got != filepath.Clean(moduleRoot) {
			t.Fatalf("got %q want %q", got, moduleRoot)
		}
	})

	t.Run("显式根目录优先", func(t *testing.T) {
		override := t.TempDir()
		want, err := filepath.Abs(override)
		if err != nil {
			t.Fatal(err)
		}
		if got := selectRuntimeRoot(cwd, "", override); got != filepath.Clean(want) {
			t.Fatalf("got %q want %q", got, want)
		}
	})
}

func TestCommonCommandEntrypointsInitializeRuntime(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join(RuntimeRoot(), "cmd"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		files, err := filepath.Glob(filepath.Join(RuntimeRoot(), "cmd", entry.Name(), "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		var source strings.Builder
		for _, file := range files {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			source.Write(data)
		}
		text := source.String()
		if !strings.Contains(text, "func main(") ||
			!strings.Contains(text, `"github.com/injoyai/strategy-tail"`) {
			continue
		}
		if !strings.Contains(text, "common.MustInitialize()") {
			t.Errorf("cmd/%s 使用 common 但未显式初始化运行时", entry.Name())
		}
	}
}
