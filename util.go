package main

import (
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/injoyai/goutil/oss"
	"github.com/injoyai/logs"
	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/lib/xorms"
	"github.com/injoyai/tdx/protocol"
)

func getDayKlines(code string, start, end time.Time) (extend.Klines, error) {
	ks, err := Pull.DayKlines(code)
	if err != nil {
		return nil, err
	}
	ls := extend.Klines{}
	for _, k := range ks {
		if k.Time.Before(start) || k.Time.After(end) {
			continue
		}
		ls = append(ls, k)
	}
	return ls, nil
}

func getMinKlines(code string, start, end time.Time) (protocol.Klines, error) {
	years := []int(nil)
	for i := start.Year(); i <= end.Year(); i++ {
		years = append(years, i)
	}
	ks := protocol.Klines{}
	mu := sync.Mutex{}
	wg := sync.WaitGroup{}
	for _, year := range years {
		wg.Add(1)
		go func(code string, year int) {
			defer wg.Done()
			filename := filepath.Join(MinKlineDir, code, code+"-"+strconv.Itoa(year)+".db")
			if !oss.Exists(filename) {
				return
			}
			db, err := xorms.NewSqlite(filename)
			if err != nil {
				logs.Err(err)
				return
			}
			defer db.Close()
			ls := protocol.Klines{}
			err = db.Find(&ls)
			if err != nil {
				logs.Err(err)
				return
			}
			res := protocol.Klines{}
			for _, l := range ls {
				if l.Time.Year() != year {
					continue
				}
				res = append(res, l)
			}
			mu.Lock()
			defer mu.Unlock()
			ks = append(ks, res...)
		}(code, year)
	}
	wg.Wait()
	ks.Sort()
	return ks, nil
}
