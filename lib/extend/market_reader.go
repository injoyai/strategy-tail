package extend

import (
	"time"

	"github.com/injoyai/strategy-tail/internal/marketstore"
	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/protocol"
)

type marketKlineSource interface {
	QueryDay(code model.Code, start, end string) ([]*model.KlineDay, error)
	QueryMinute(code model.Code, start, end string) ([]*model.KlineMinute, error)
}

// MarketReader 将已入库的行情转换为回测用 K 线。
type MarketReader struct {
	store *marketstore.MySQLReader
}

// OpenMarketReader 打开现有 MySQL 行情库供研究读取。
func OpenMarketReader(dsn string) (*MarketReader, error) {
	store, err := marketstore.OpenMySQLReader(dsn)
	if err != nil {
		return nil, err
	}
	return &MarketReader{store: store}, nil
}

func (r *MarketReader) Close() error { return r.store.Close() }

// StorageIdentity 返回不含账号和密码的行情库标识。
func (r *MarketReader) StorageIdentity() string { return r.store.Identity() }

// DayKlines 按源日期闭区间读取日线。股本字段取自日线存储。
func (r *MarketReader) DayKlines(code string, start, end time.Time) (Klines, error) {
	return readDayKlines(r.store, nil, code, start, end)
}

// MinKlines 读取 start 到 end 所在年份的整年分钟线。
func (r *MarketReader) MinKlines(code string, start, end time.Time) (protocol.Klines, error) {
	return readMinKlines(r.store, code, start, end)
}

// LocalStockCodes 枚举已入库的股票代码，含库中保留的退市代码。
func (r *MarketReader) LocalStockCodes() ([]string, error) {
	return localStockCodes(r.store, r.StorageIdentity())
}
