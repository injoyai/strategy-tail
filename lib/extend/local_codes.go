package extend

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/injoyai/tdx/v2/protocol"
)

// LocalStockCodes 从日线库文件名枚举股票，保留已退市但仍有本地数据的代码。
func (p *PullKline) LocalStockCodes() ([]string, error) {
	dir := filepath.Join(p.Config.Dir, DirDay)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("读取本地股票代码 %s: %w", dir, err)
	}
	codes := make([]string, 0, len(entries))
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
		codes = append(codes, code)
	}
	if len(codes) == 0 {
		return nil, fmt.Errorf("本地日线库没有股票数据: %s", dir)
	}
	return codes, nil
}
