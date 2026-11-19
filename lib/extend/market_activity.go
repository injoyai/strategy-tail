package extend

import (
	"context"
	"errors"

	"github.com/injoyai/strategy-tail/internal/marketstore"
	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/protocol"
)

// LocalIndexCodes lists the indices with stored daily bars.
func (r *MarketReader) LocalIndexCodes() ([]model.Code, error) {
	return r.source.DayCodes(protocol.MarketIndex)
}

func (r *MarketReader) ReadStockTurnover(ctx context.Context, from, to string, visit func(marketstore.StockTurnover)) error {
	source, ok := r.source.(interface {
		ReadStockTurnover(context.Context, string, string, func(marketstore.StockTurnover)) error
	})
	if !ok {
		return errors.New("当前行情源不支持读取股票成交数据")
	}
	return source.ReadStockTurnover(ctx, from, to, visit)
}

// SectorHistory returns block closes for the five sessions before date.
func (r *MarketReader) SectorHistory(ctx context.Context, date string) ([]string, []marketstore.SectorDayClose, error) {
	source, ok := r.source.(interface {
		SectorHistory(context.Context, string) ([]string, []marketstore.SectorDayClose, error)
	})
	if !ok {
		return nil, nil, errors.New("行情源不支持板块历史")
	}
	return source.SectorHistory(ctx, date)
}
