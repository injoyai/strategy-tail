package common

import (
	"os"
	"path/filepath"
	"testing"
)

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
