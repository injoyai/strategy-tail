package extend

import (
	"fmt"
	"sort"

	"github.com/injoyai/tdx/v2/protocol"
)

// LocalStockCodes 枚举行情库中有日线的股票，保留退市代码。
func (r *MarketReader) LocalStockCodes() ([]string, error) {
	store := r.source
	identity := r.StorageIdentity()
	stored, err := store.DayCodes(protocol.MarketAStock)
	if err != nil {
		return nil, fmt.Errorf("读取行情股票代码: %w", err)
	}
	seen := map[string]bool{}
	var codes []string
	for _, item := range stored {
		if protocol.IsStock(item.Code) && !seen[item.Code] {
			codes = append(codes, item.Code)
			seen[item.Code] = true
		}
	}
	if len(codes) == 0 {
		return nil, fmt.Errorf("行情存储没有股票日线: %s", identity)
	}
	sort.Strings(codes)
	return codes, nil
}
