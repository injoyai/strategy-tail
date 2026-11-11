package marketstore

import (
	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/protocol"
)

// MySQLReader 查询现有行情分表，打开与查询时均不创建表。
type MySQLReader struct {
	store *MySQLStore
}

func OpenMySQLReader(dsn string) (*MySQLReader, error) {
	store, err := openMySQLStore(dsn, false, true)
	if err != nil {
		return nil, err
	}
	return &MySQLReader{store: store}, nil
}

func (r *MySQLReader) Close() error { return r.store.Close() }

func (r *MySQLReader) Identity() string { return r.store.Identity() }

func (r *MySQLReader) QueryDay(code model.Code, start, end string) ([]*model.KlineDay, error) {
	return r.store.queryDay(code, start, end, true)
}

func (r *MySQLReader) QueryDayRecords(code model.Code, start, end string) ([]*DayRecord, error) {
	return r.store.queryDayRecords(code, start, end, true)
}

func (r *MySQLReader) QueryMinute(code model.Code, start, end string) ([]*model.KlineMinute, error) {
	return r.store.QueryMinute(code, start, end)
}

func (r *MySQLReader) DayCodes(market protocol.Market) ([]model.Code, error) {
	return r.store.dayCodes(market, true)
}

func (r *MySQLReader) LastDayTime(code model.Code) (string, error) {
	return r.store.lastDayTime(code, true)
}
