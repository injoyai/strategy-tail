package extend

import (
	"errors"
	"time"

	"github.com/injoyai/strategy-tail/internal/marketstore"
	"github.com/injoyai/strategy-tail/researchdata"
	"github.com/injoyai/tdx/v2/extend/metadata"
	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/protocol"
)

var ErrNotEnoughKline = errors.New("K 线数据不足")

// MarketSource describes the raw, read-only bar queries used by strategies.
type MarketSource interface {
	Identity() string
	QueryDay(model.Code, string, string) ([]*model.KlineDay, error)
	QueryMinute(model.Code, string, string) ([]*model.KlineMinute, error)
	DayCodes(protocol.Market) ([]model.Code, error)
	LastDayTime(model.Code) (string, error)
}

type MarketReader struct {
	source   MarketSource
	mysql    *marketstore.MySQLReader
	metadata *MarketData
	equity   func(string, time.Time) *protocol.Equity
}

func NewMarketReader(source MarketSource, equity func(string, time.Time) *protocol.Equity) *MarketReader {
	return &MarketReader{source: source, equity: equity}
}

func OpenMarketReader(dsn string) (*MarketReader, error) {
	store, err := marketstore.OpenMySQLReader(dsn)
	if err != nil {
		return nil, err
	}
	data, err := readMarketMetadata(store)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	r := NewMarketReader(store, data.Equity.GetEquity)
	r.mysql, r.metadata = store, data
	return r, nil
}

func readMarketMetadata(store *marketstore.MySQLReader) (*MarketData, error) {
	codes, err := store.Codes()
	if err != nil {
		return nil, err
	}
	codeIndex, err := metadata.NewCodes(codes)
	if err != nil {
		return nil, err
	}
	events, err := store.EquityEvents()
	if err != nil {
		return nil, err
	}
	equity, err := metadata.NewGbbq(events)
	if err != nil {
		return nil, err
	}
	return &MarketData{Codes: codeIndex, Equity: equity}, nil
}

func (r *MarketReader) Close() error {
	if r.mysql != nil {
		return r.mysql.Close()
	}
	return nil
}

func (r *MarketReader) StorageIdentity() string                  { return r.source.Identity() }
func (r *MarketReader) Metadata() *MarketData                    { return r.metadata }
func (r *MarketReader) ResearchView() (researchdata.View, error) { return r.mysql.ResearchView() }

func (r *MarketReader) EquityAt(code string, at time.Time) *protocol.Equity {
	if r.equity == nil {
		return nil
	}
	return r.equity(code, at)
}

func (r *MarketReader) MembershipUniverse() (researchdata.Universe, error) {
	return r.mysql.MembershipUniverse()
}

func (r *MarketReader) DayKlinesAll(code string) (Klines, error) {
	return r.DayKlines(code, time.Time{}, time.Time{})
}

func (r *MarketReader) DayKline(code string, n ...int) (*Kline, error) {
	bars, err := r.DayKlinesAll(code)
	if err != nil {
		return nil, err
	}
	i := -1
	if len(n) > 0 {
		i = n[0]
	}
	if i < 0 {
		i += len(bars)
	}
	if i < 0 || i >= len(bars) {
		return nil, ErrNotEnoughKline
	}
	return bars[i], nil
}

func (r *MarketReader) LatestDayDate(code string) (string, error) {
	c, err := standardSourceCode(code)
	if err != nil {
		return "", err
	}
	return r.source.LastDayTime(c)
}

func (r *MarketReader) StockSessionBars(codes []string, now time.Time) (map[string]*protocol.Kline, error) {
	if r.mysql == nil {
		return nil, errors.New("盘中行情需要 MySQL 行情源")
	}
	bars, err := r.mysql.StockSessionBars(codes, now)
	if err != nil {
		return nil, err
	}
	out := make(map[string]*protocol.Kline, len(bars))
	for code, bar := range bars {
		at, err := time.ParseInLocation(protocol.TimeLayout, bar.Time, protocol.Location())
		if err != nil {
			return nil, err
		}
		vol, err := sourceVolume(model.Code{Market: protocol.MarketAStock, Code: code}, bar.Volume)
		if err != nil {
			return nil, err
		}
		out[code] = &protocol.Kline{Time: at, Open: bar.Open, High: bar.High, Low: bar.Low, Close: bar.Close, Volume: vol, Amount: amountValue(bar.Amount)}
	}
	return out, nil
}
