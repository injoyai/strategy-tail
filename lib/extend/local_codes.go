package extend

import (
	"fmt"
	"sort"

	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/protocol"
)

// LocalStockCodes 枚举行情库中有日线的股票，保留退市代码。
func (p *PullKline) LocalStockCodes() ([]string, error) {
	if p.klineSource() == nil {
		return nil, ErrMarketStoreRequired
	}
	store, ok := p.klineSource().(dayCodeSource)
	if !ok {
		return nil, fmt.Errorf("行情存储不支持股票枚举: %s", p.StorageIdentity())
	}
	return localStockCodes(store, p.StorageIdentity())
}

type dayCodeSource interface {
	DayCodes(protocol.Market) ([]model.Code, error)
}

func localStockCodes(store dayCodeSource, identity string) ([]string, error) {
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
