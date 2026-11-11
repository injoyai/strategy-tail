package main

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/protocol"
)

// readCSVSnapshot keeps at most one calendar year in memory. Equal timestamps
// keep the last source row, matching KlineMinute1's Date/ID ordering contract.
func readCSVSnapshot(file string, emit func([]*model.KlineMinute) error) (receipt, error) {
	var stats receipt
	f, err := os.Open(file)
	if err != nil {
		return stats, err
	}
	defer f.Close()
	reader := csv.NewReader(f)
	header, err := reader.Read()
	if errors.Is(err, io.EOF) {
		return stats, nil
	}
	if err != nil {
		return stats, err
	}
	for n := range header {
		header[n] = strings.TrimSpace(strings.TrimPrefix(header[n], "\ufeff"))
	}
	if !reflect.DeepEqual(header, []string{"日期", "时间", "开盘", "最高", "最低", "收盘", "成交量", "成交额"}) {
		return stats, errors.New("CSV 表头不匹配：需要日期、时间、OHLC、手成交量和原币种成交额")
	}
	reader.FieldsPerRecord = 8
	info, _ := protocol.MarketEtfLof.Info()
	var rows []*model.KlineMinute
	previous := ""
	flush := func() error {
		if emit != nil && len(rows) > 0 {
			return emit(rows)
		}
		return nil
	}
	for line := 2; ; line++ {
		raw, e := reader.Read()
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			return stats, fmt.Errorf("CSV 第 %d 行：%w", line, e)
		}
		for n := range raw {
			raw[n] = strings.TrimSpace(raw[n])
		}
		layout := protocol.TimeLayout
		if len(raw[1]) == 8 {
			layout = "2006-01-02 15:04:05"
		}
		at, e := time.ParseInLocation(layout, raw[0]+" "+raw[1], protocol.Location())
		if e != nil || at.Second() != 0 || at.Year() < 1990 || at.Year() > 2100 {
			return stats, fmt.Errorf("CSV 第 %d 行日期或分钟非法", line)
		}
		k := &model.KlineMinute{Time: at.Format(protocol.TimeLayout)}
		if previous != "" && k.Time < previous {
			return stats, fmt.Errorf("CSV 第 %d 行时间倒序，未猜测排序", line)
		}
		var amount float64
		for n, dest := range []*float64{&k.Open, &k.High, &k.Low, &k.Close, &amount} {
			column := n + 2
			if n == 4 {
				column = 7
			}
			v, e := strconv.ParseFloat(raw[column], 64)
			if e != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
				return stats, fmt.Errorf("CSV 第 %d 行第 %d 列数值非法", line, column+1)
			}
			*dest = v
		}
		k.Amount = &amount
		volume, e := strconv.ParseInt(raw[6], 10, 64)
		if e != nil {
			return stats, fmt.Errorf("CSV 第 %d 行成交量不是整数手", line)
		}
		if k.Volume, e = info.Minute.ToStorageVolume(volume); e != nil {
			return stats, fmt.Errorf("CSV 第 %d 行：%w", line, e)
		}
		if len(rows) > 0 && rows[0].Time[:4] != k.Time[:4] {
			if e = flush(); e != nil {
				return stats, e
			}
			rows = nil
		}
		if len(rows) == 0 {
			stats.Years = append(stats.Years, at.Year())
		}
		if k.Time == previous {
			rows[len(rows)-1] = k
			stats.Duplicates++
		} else {
			rows = append(rows, k)
			stats.Rows++
		}
		if stats.First == "" {
			stats.First = k.Time
		}
		stats.Last, previous = k.Time, k.Time
	}
	return stats, flush()
}
