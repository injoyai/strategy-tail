package extend

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/injoyai/tdx/v2/protocol"
)

// LocalStockCodes 从日线库文件名枚举股票，保留已退市但仍有本地数据的代码。
func (p *PullKline) LocalStockCodes() ([]string, error) {
	dir := filepath.Join(p.Config.Dir, DirDay)
	dirs := []string{dir, filepath.Join(p.Config.Dir, SourceDir, protocol.MarketAStock.String(), "day")}
	seen := map[string]bool{}
	var codes []string
	found := false
	var missing error
	for _, name := range dirs {
		entries, err := os.ReadDir(name)
		if errors.Is(err, os.ErrNotExist) {
			missing = err
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("读取本地股票代码 %s: %w", name, err)
		}
		found = true
		for _, entry := range entries {
			if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".db") {
				continue
			}
			code := strings.TrimSuffix(entry.Name(), ".db")
			if !protocol.IsStock(code) {
				continue
			}
			if _, err := strconv.ParseUint(code[2:], 10, 32); err != nil {
				continue
			}
			if !seen[code] {
				codes = append(codes, code)
				seen[code] = true
			}
		}
	}
	if !found {
		return nil, fmt.Errorf("读取本地股票代码: %w", missing)
	}
	sort.Strings(codes)
	if len(codes) == 0 {
		return nil, fmt.Errorf("本地日线库没有股票数据: %s", dir)
	}
	return codes, nil
}
