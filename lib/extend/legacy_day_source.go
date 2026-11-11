package extend

import (
	"sort"
	"time"

	"github.com/injoyai/strategy-tail/internal/marketstore"
	"github.com/injoyai/strategy-tail/internal/xorms"
	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/protocol"
)

// ReadLegacyDaySource 读取旧 Unix 主键日线库，转换为源时间日线行。
//
// 旧库价格与成交额为整数毫（×1000），成交量以手为单位；源时间存储使用
// 原币种浮点价和股，因此这里按列类型（INT/REAL）判断后除以 1000，
// 并把成交量乘以 SharesPerLot。换手率（%）与股本（股）原样保留。
//
// 同一交易日出现多行时保留最后一行（Unix 升序），避免在导入时用旧值覆盖同日更新后的记录。
func ReadLegacyDaySource(filename string) ([]*marketstore.DayRecord, error) {
	db, err := xorms.OpenReadOnly(filename)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows := Klines{}
	if err := db.Asc("Unix").Find(&rows); err != nil {
		return nil, err
	}
	if err := normalizeLegacyDays(db.DB().DB, rows); err != nil {
		return nil, err
	}

	byDate := map[string]*marketstore.DayRecord{}
	for _, k := range rows {
		if k == nil || k.Kline == nil {
			continue
		}
		date := time.Unix(k.Unix, 0).In(protocol.Location()).Format(time.DateOnly)
		byDate[date] = &marketstore.DayRecord{KlineDay: model.KlineDay{
			Time:   date,
			Open:   k.Open,
			High:   k.High,
			Low:    k.Low,
			Close:  k.Close,
			Volume: protocol.ToShares(k.Volume),
			Amount: &k.Amount,
		},
			Turnover:   k.Turnover,
			FloatStock: float64(k.FloatStock),
			TotalStock: float64(k.TotalStock),
		}
	}

	dates := make([]string, 0, len(byDate))
	for date := range byDate {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	out := make([]*marketstore.DayRecord, 0, len(dates))
	for _, date := range dates {
		out = append(out, byDate[date])
	}
	return out, nil
}
