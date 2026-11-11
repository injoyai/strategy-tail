package extend

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/injoyai/strategy-tail/internal/marketstore"
	"github.com/injoyai/tdx/v2/extend/metadata"
	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/extend/pull"
	_ "github.com/injoyai/tdx/v2/extend/pull/market"
	"github.com/injoyai/tdx/v2/protocol"
)

// The application owns progress, retry reporting and valuation; the SDK owns fetch/merge/storage.
func fetchSource(s *pull.Service, m *MarketData, code, kind string) error {
	c, err := pull.ParseCode(code)
	if err != nil {
		return err
	}
	info, _ := c.Market.Info()
	if info.Protocol != protocol.ProtocolStandard {
		return fmt.Errorf("当前应用更新器仅支持标准市场，扩展市场请通过 SDK pull 配置 ExPool: %s", code)
	}
	if m.ClientPool == nil {
		return fmt.Errorf("标准行情连接池尚未初始化")
	}
	unit, ok := pull.Get(c.Market.String())
	if !ok {
		return fmt.Errorf("未注册市场 %s", c.Market)
	}
	if kind == Day {
		return unit.FetchDay(s, c)
	}
	return unit.FetchMinute(s, c)
}

func (p *PullKline) sourceUpdateKey() string {
	identity := p.StorageIdentity()
	codes := append([]string(nil), p.Config.Codes...)
	sort.Strings(codes)
	kinds := append([]string(nil), p.Types...)
	sort.Strings(kinds)
	b, _ := json.Marshal([]any{identity, codes, kinds, p.Config.StartAt.Format(time.DateOnly)})
	return fmt.Sprintf("source:%x", sha256.Sum256(b))
}

// StorageIdentity 不含凭据，供更新状态与运行日志确认实际行情目标。
func (p *PullKline) StorageIdentity() string {
	if p.reader != nil {
		return p.reader.Identity()
	}
	if p.Config.Store != nil {
		return p.Config.Store.Identity()
	}
	return ""
}

// Source date labels become A-share execution timestamps only at this strategy boundary.
func standardSourceCode(code string) (model.Code, error) {
	c, err := pull.ParseCode(code)
	if err != nil {
		return c, err
	}
	info, _ := c.Market.Info()
	if info.Protocol != protocol.ProtocolStandard {
		return c, fmt.Errorf("当前回测模型仅支持标准市场，无法解释扩展行情源时间: %s", code)
	}
	return c, nil
}

func sourceVolume(code model.Code, v int64) (int64, error) {
	// The standard index decoder already uses the same units as the SDK's
	// stored index volume. Stock bars use shares in storage and lots here.
	if v >= 0 && (code.Market == protocol.MarketIndex || code.Market == protocol.MarketBlock) {
		return v, nil
	}
	if v < 0 || v%protocol.SharesPerLot != 0 {
		return 0, fmt.Errorf("成交量 %d 股无法无损转换为整数手", v)
	}
	return protocol.FromShares(v), nil
}

func sourceStock(v float64) (int64, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v >= float64(math.MaxInt64) || math.Trunc(v) != v {
		return 0, fmt.Errorf("股本 %v 不能转换为整数股", v)
	}
	return int64(v), nil
}

func (p *PullKline) dayKlines(code string, start, end time.Time) (Klines, error) {
	return readDayKlines(p.klineSource(), p.Config.Equity, code, start, end)
}

func readDayKlines(store marketKlineSource, getEquity func() *metadata.Gbbq, code string, start, end time.Time) (Klines, error) {
	c, err := standardSourceCode(code)
	if err != nil {
		return nil, err
	}
	if store == nil {
		return nil, ErrMarketStoreRequired
	}
	endDate := ""
	if !end.IsZero() {
		endDate = end.Format(time.DateOnly)
	}

	var records []*marketstore.DayRecord
	if extra, ok := store.(interface {
		QueryDayRecords(model.Code, string, string) ([]*marketstore.DayRecord, error)
	}); ok {
		records, err = extra.QueryDayRecords(c, "", endDate)
	} else {
		var rows []*model.KlineDay
		rows, err = store.QueryDay(c, "", endDate)
		for _, r := range rows {
			records = append(records, &marketstore.DayRecord{KlineDay: *r})
		}
	}
	if err != nil {
		return nil, err
	}
	var equity *metadata.Gbbq
	if getEquity != nil {
		equity = getEquity()
	}
	out := make(Klines, 0, len(records))
	var last float64
	for _, r := range records {
		at, err := time.ParseInLocation(time.DateOnly, r.Time, protocol.Location())
		if err != nil {
			return nil, err
		}
		at = at.Add(15 * time.Hour)
		volume, err := sourceVolume(c, r.Volume)
		if err != nil {
			return nil, err
		}
		floating, err := sourceStock(r.FloatStock)
		if err != nil {
			return nil, err
		}
		total, err := sourceStock(r.TotalStock)
		if err != nil {
			return nil, err
		}
		if equity != nil && c.Market == protocol.MarketAStock && (floating == 0 || total == 0) {
			if v := equity.GetEquity(c.Code, at); v != nil {
				if floating == 0 {
					floating = v.Float
				}
				if total == 0 {
					total = v.Total
				}
			}
		}
		turnover := r.Turnover
		if turnover == 0 && floating > 0 {
			turnover = float64(r.Volume) / float64(floating) * 100
		}
		out = append(out, &Kline{Unix: at.Unix(), Kline: &protocol.Kline{Time: at, Last: last, Open: r.Open, High: r.High, Low: r.Low, Close: r.Close, Volume: volume, Amount: amountValue(r.Amount)}, Turnover: turnover, FloatStock: floating, TotalStock: total})
		last = r.Close
	}
	return filterSourceDays(out, start, end), nil
}

func (p *PullKline) DayKlinesAll(code string) (Klines, error) {
	return p.dayKlines(code, time.Time{}, time.Time{})
}

func (p *PullKline) DayKlines(code string, start, end time.Time) (Klines, error) {
	return p.dayKlines(code, start, end)
}

func filterSourceDays(rows Klines, start, end time.Time) Klines {
	out := Klines{}
	for _, r := range rows {
		d := r.Time.In(protocol.Location()).Format(time.DateOnly)
		if !start.IsZero() && d < start.Format(time.DateOnly) || !end.IsZero() && d > end.Format(time.DateOnly) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func (p *PullKline) MinKlines(code string, start, end time.Time) (protocol.Klines, error) {
	return readMinKlines(p.klineSource(), code, start, end)
}

func readMinKlines(store marketKlineSource, code string, start, end time.Time) (protocol.Klines, error) {
	c, err := standardSourceCode(code)
	if err != nil {
		return nil, err
	}
	if store == nil {
		return nil, ErrMarketStoreRequired
	}
	// Preserve the strategy API's whole-year minute loading.
	rows, err := store.QueryMinute(c, fmt.Sprintf("%04d-01-01 00:00", start.Year()), fmt.Sprintf("%04d-12-31 23:59", end.Year()))
	if err != nil {
		return nil, err
	}
	out := make(protocol.Klines, 0, len(rows))
	for _, r := range rows {
		at, err := time.ParseInLocation(protocol.TimeLayout, r.Time, protocol.Location())
		if err != nil {
			return nil, err
		}
		volume, err := sourceVolume(c, r.Volume)
		if err != nil {
			return nil, err
		}
		out = append(out, &protocol.Kline{Time: at, Open: r.Open, High: r.High, Low: r.Low, Close: r.Close, Volume: volume, Amount: amountValue(r.Amount)})
	}
	return out, nil
}

func amountValue(amount *float64) float64 {
	if amount == nil {
		return 0
	}
	return *amount
}

func (p *PullKline) klineSource() marketKlineSource {
	if p.reader != nil {
		return p.reader
	}
	return p.Config.Store
}

// LatestDayDate returns the last stored session date without downloading history.
func (p *PullKline) LatestDayDate(code string) (string, error) {
	c, err := standardSourceCode(code)
	if err != nil {
		return "", err
	}
	if p.reader != nil {
		return p.reader.LastDayTime(c)
	}
	if p.Config.Store == nil {
		return "", ErrMarketStoreRequired
	}
	return p.Config.Store.LastDayTime(c)
}
