package lab

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// 独立计算模块禁止依赖应用装配；只有 builder 沿用现有 common 数据注入。
func TestModuleDependencyBoundaries(t *testing.T) {
	const module = "github.com/injoyai/strategy-tail"
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate module sources")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(source)))
	for _, tc := range []struct {
		dir     string
		allowed []string // 非标准库依赖的完整包路径；空列表表示纯标准库。
	}{
		{dir: "internal/lab/factorstats"},
		{dir: "internal/portfolioresearch"},
		{dir: "internal/lab/builder", allowed: []string{module, module + "/core", module + "/strategies/buy", module + "/strategies/sell", module + "/strategies/factor"}},
		{dir: "internal/lab/script", allowed: []string{module + "/core", module + "/strategies/buy", module + "/strategies/sell", module + "/strategies/factor", "github.com/traefik/yaegi/interp", "github.com/traefik/yaegi/stdlib"}},
		{dir: "internal/researchrun", allowed: []string{module + "/core", module + "/lib/extend", "github.com/injoyai/logs", "github.com/injoyai/tdx/protocol"}},
		{dir: "strategies/factor", allowed: []string{module + "/core", module + "/lib/extend", module + "/researchdata", module + "/strategies/util", "github.com/injoyai/tdx/protocol"}},
		{dir: "strategies/rule", allowed: []string{module + "/core", module + "/lib/extend", module + "/researchdata", module + "/strategies/factor", "github.com/injoyai/tdx/protocol"}},
	} {
		t.Run(tc.dir, func(t *testing.T) {
			entries, err := os.ReadDir(filepath.Join(root, tc.dir))
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
					continue
				}
				filename := filepath.Join(root, tc.dir, entry.Name())
				file, err := parser.ParseFile(token.NewFileSet(), filename, nil, parser.ImportsOnly)
				if err != nil {
					t.Fatal(err)
				}
				for _, imp := range file.Imports {
					path, err := strconv.Unquote(imp.Path.Value)
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(strings.Split(path, "/")[0], ".") && !slices.Contains(tc.allowed, path) {
						t.Errorf("%s imports %s across its module boundary; keep orchestration in the caller", entry.Name(), path)
					}
				}
			}
		})
	}
}
