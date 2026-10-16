// Package tdx stores observed TDX valuation snapshots without inventing history.
package tdx

import (
	"crypto/sha256"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/injoyai/strategy-tail/researchdata"
	upstream "github.com/injoyai/tdx"
	"github.com/injoyai/tdx/protocol"
)

// Keep this contract compatible with the initial manually collected snapshot.
var valuationSpec = researchdata.DatasetSpec{
	ID: "tdx.valuation.daily", Kind: researchdata.KindFundamental, Source: "tdx", Version: "1",
	Description: "通达信盘后估值快照；以实际采集时间为可得时间，不回填历史可得性",
	Fields: []researchdata.FieldSpec{
		{Name: "pe_ttm", Unit: "multiple", Description: "通达信滚动市盈率；零值或无效原始字段不入库"},
		{Name: "pe_static", Unit: "multiple", Description: "通达信静态市盈率；零值或无效原始字段不入库"},
		{Name: "dividend_yield", Unit: "percent", Description: "通达信口径股息率；3表示3%，不同于其他供应商口径"},
	},
}

// Sync downloads one full-market snapshot and stores only Shanghai/Shenzhen
// mainboard rows. The second count reports rejected mainboard rows.
func Sync(pool upstream.IPool, store *researchdata.SQLiteStore) (int, int, error) {
	if pool == nil || store == nil {
		return 0, 0, fmt.Errorf("TDX估值更新器尚未初始化")
	}
	var stats []*protocol.TdxStat
	err := pool.Do(func(c *upstream.Client) error {
		var err error
		stats, err = c.GetTdxStat()
		return err
	})
	if err != nil {
		return 0, 0, err
	}
	records, skipped, err := normalizeSnapshot(stats, time.Now())
	if err != nil {
		return 0, skipped, err
	}
	if err = store.RegisterDataset(valuationSpec); err != nil {
		return 0, skipped, err
	}
	if err = store.Ingest(records); err != nil {
		return 0, skipped, err
	}
	return len(records), skipped, nil
}

func normalizeSnapshot(stats []*protocol.TdxStat, observedAt time.Time) ([]researchdata.Record, int, error) {
	if observedAt.IsZero() {
		return nil, 0, fmt.Errorf("缺少估值采集时间")
	}
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return nil, 0, err
	}
	var records []researchdata.Record
	skipped := 0
	seen := map[string]bool{}
	for _, s := range stats {
		if s == nil {
			skipped++
			continue
		}
		prefix := ""
		switch {
		case s.Market == 1 && strings.HasPrefix(s.Code, "60"):
			prefix = "sh"
		case s.Market == 0 && strings.HasPrefix(s.Code, "00"):
			prefix = "sz"
		default:
			continue
		}
		_, codeErr := strconv.ParseUint(s.Code, 10, 32)
		date, dateErr := time.ParseInLocation("20060102", s.Date, loc)
		eventAt := time.Date(date.Year(), date.Month(), date.Day(), 15, 0, 0, 0, loc)
		if len(s.Code) != 6 || codeErr != nil || dateErr != nil || eventAt.After(observedAt) {
			skipped++
			continue
		}
		values := map[string]float64{}
		for _, field := range []struct {
			name  string
			index int
		}{{"pe_ttm", 3}, {"pe_static", 9}, {"dividend_yield", 10}} {
			if field.index >= len(s.Fields) {
				continue
			}
			value, err := strconv.ParseFloat(strings.TrimSpace(s.Fields[field.index]), 64)
			if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
				continue
			}
			if (field.name != "dividend_yield" && value == 0) || (field.name == "dividend_yield" && value < 0) {
				continue
			}
			values[field.name] = value
		}
		if len(values) == 0 {
			skipped++
			continue
		}
		code := prefix + s.Code
		if seen[code] {
			return nil, skipped, fmt.Errorf("TDX估值快照证券重复: %s", code)
		}
		seen[code] = true
		records = append(records, researchdata.Record{
			Dataset: valuationSpec.ID, Code: code, Key: date.Format(time.DateOnly), Source: valuationSpec.Source,
			Revision: fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(s.Fields, "|")))),
			EventAt:  eventAt, AvailableAt: observedAt, Values: values,
			Attributes: map[string]string{"availability_basis": "observed_at_fetch", "dividend_yield_basis": "tdx_vendor_defined_percent", "source_file": "tdxstat.cfg", "pit_status": "snapshot_observed_no_historical_backfill"},
		})
	}
	if len(records) == 0 {
		return nil, skipped, fmt.Errorf("TDX快照没有日期和数值有效的沪深主板估值")
	}
	return records, skipped, nil
}
