package extend

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/injoyai/tdx/v2/extend/manage"
	"github.com/injoyai/tdx/v2/extend/pull"
	_ "github.com/injoyai/tdx/v2/extend/pull/market"
	"github.com/injoyai/tdx/v2/protocol"
)

// SourceDir is separate from legacy Unix-key databases; SDK writes only source strings.
const SourceDir = "source"

func (p *PullKline) openSourceStore(create bool) (pull.Store, func() error, error) {
	if p.Config.Store != nil {
		return p.Config.Store, nil, nil
	}
	dir := filepath.Join(p.Config.Dir, SourceDir)
	if !create {
		if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil
		} else if err != nil {
			return nil, nil, err
		}
		if _, err := os.Stat(filepath.Join(dir, ".time-format")); err != nil {
			return nil, nil, fmt.Errorf("源时间目录格式标记不可读: %w", err)
		}
	}
	s, err := pull.NewSQLiteStore(dir, 4)
	if err != nil {
		return nil, nil, err
	}
	return s, s.Close, nil
}

// The application owns progress, retry reporting and valuation; the SDK owns fetch/merge/storage.
func fetchSource(s *pull.Service, m *manage.Manage, code, kind string) error {
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
	identity := filepath.Join(p.Config.Dir, SourceDir)
	if p.Config.Store != nil {
		identity = p.Config.Store.Identity()
	}
	codes := append([]string(nil), p.Config.Codes...)
	sort.Strings(codes)
	kinds := append([]string(nil), p.Types...)
	sort.Strings(kinds)
	b, _ := json.Marshal([]any{identity, codes, kinds, p.Config.StartAt.Format(time.DateOnly)})
	return fmt.Sprintf("source:%x", sha256.Sum256(b))
}

// Source date labels become A-share execution timestamps only at this strategy boundary.
func standardSourceCode(code string) (pull.Code, error) {
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

func sourceVolume(v int64) (int64, error) {
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
	c, err := standardSourceCode(code)
	if err != nil {
		return nil, err
	}
	old, err := p.legacyDayKlines(c.Code, start, end)
	if err != nil {
		return nil, err
	}
	store, closeStore, err := p.openSourceStore(false)
	if err != nil {
		return nil, err
	}
	if store == nil {
		return filterSourceDays(old, start, end), nil
	}
	if closeStore != nil {
		defer closeStore()
	}
	endDate := ""
	if !end.IsZero() {
		endDate = end.Format(time.DateOnly)
	}
	rows, err := store.QueryDay(c, "", endDate)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return filterSourceDays(old, start, end), nil
	}
	byDate := map[string]*Kline{}
	for _, k := range old {
		byDate[k.Time.In(protocol.Location()).Format(time.DateOnly)] = k
	}
	newer := map[string]bool{}
	for _, r := range rows {
		at, e := time.ParseInLocation(time.DateOnly, r.Time, protocol.Location())
		if e != nil {
			return nil, e
		}
		at = at.Add(15 * time.Hour) // Existing standard-market strategies execute daily signals at close.
		if previous := byDate[r.Time]; previous != nil {
			at = previous.Time
		}
		volume, e := sourceVolume(r.Volume)
		if e != nil {
			return nil, e
		}
		floating, e := sourceStock(r.FloatStock)
		if e != nil {
			return nil, e
		}
		total, e := sourceStock(r.TotalStock)
		if e != nil {
			return nil, e
		}
		byDate[r.Time] = &Kline{Unix: at.Unix(), Kline: &protocol.Kline{Time: at, Open: r.Open, High: r.High, Low: r.Low, Close: r.Close, Volume: volume, Amount: r.Amount}, Turnover: r.Turnover, FloatStock: floating, TotalStock: total}
		newer[r.Time] = true
	}
	dates := make([]string, 0, len(byDate))
	for date := range byDate {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	out := make(Klines, 0, len(dates))
	var last float64
	for _, date := range dates {
		k := byDate[date]
		if newer[date] {
			k.Last = last
		}
		last = k.Close
		out = append(out, k)
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
	c, err := standardSourceCode(code)
	if err != nil {
		return nil, err
	}
	old, err := p.legacyMinKlines(c.Code, start, end)
	if err != nil {
		return nil, err
	}
	store, closeStore, err := p.openSourceStore(false)
	if err != nil {
		return nil, err
	}
	if store == nil {
		return old, nil
	}
	if closeStore != nil {
		defer closeStore()
	}
	// Preserve the existing API's whole-year minute loading, including boundary duplicates.
	rows, err := store.QueryMinute(c, fmt.Sprintf("%04d-01-01 00:00", start.Year()), fmt.Sprintf("%04d-12-31 23:59", end.Year()))
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return old, nil
	}
	refreshed := map[string]*protocol.Kline{}
	for _, r := range rows {
		at, e := time.ParseInLocation(protocol.TimeLayout, r.Time, protocol.Location())
		if e != nil {
			return nil, e
		}
		volume, e := sourceVolume(r.Volume)
		if e != nil {
			return nil, e
		}
		refreshed[r.Time] = &protocol.Kline{Time: at, Open: r.Open, High: r.High, Low: r.Low, Close: r.Close, Volume: volume, Amount: r.Amount}
	}
	out := make(protocol.Klines, 0, len(old)+len(rows))
	for _, r := range old {
		if _, ok := refreshed[r.Time.In(protocol.Location()).Format(protocol.TimeLayout)]; !ok {
			out = append(out, r)
		}
	}
	for _, r := range rows {
		out = append(out, refreshed[r.Time])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out, nil
}
