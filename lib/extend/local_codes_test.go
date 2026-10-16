package extend

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLocalStockCodes(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, DirDay)
	if err := os.MkdirAll(filepath.Join(dir, "sh600003.db"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"sz000003.db", // 已退市股票：只需保留本地库，不需要当前代码表。
		"sh600000.db", "sz300001.db", "sh688001.db", "bj920001.db",
		"sh000001.db", "sh000300.db", "sz399001.db", "sz159001.db",
		"update.db", "sh600000.db-wal", "sh600000.db-shm", "sh600000.db.bak",
		"sh60000x.db", "sz000+01.db", "sh60001.db",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	p := &PullKline{Config: PullKlineConfig{Dir: root}}
	got, err := p.LocalStockCodes()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"bj920001", "sh600000", "sh688001", "sz000003", "sz300001"}
	if !slices.Equal(got, want) {
		t.Fatalf("LocalStockCodes() = %v, want %v", got, want)
	}
}

func TestLocalStockCodesMissingOrEmpty(t *testing.T) {
	root := t.TempDir()
	p := &PullKline{Config: PullKlineConfig{Dir: root}}
	if _, err := p.LocalStockCodes(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("日线目录缺失应返回文件错误: %v", err)
	}
	dir := filepath.Join(root, DirDay)
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sh000001.db"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := p.LocalStockCodes(); err == nil {
		t.Fatal("只有指数、没有股票的目录应报错")
	}
}
