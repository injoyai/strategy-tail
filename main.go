package main

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/injoyai/goutil/oss"
	"github.com/injoyai/goutil/str/bar/v2"
	"github.com/injoyai/logs"
	"github.com/injoyai/tdx"
	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/lib/xorms"
	"github.com/injoyai/tdx/protocol"
)

var (
	DatabaseDir = tdx.DefaultDatabaseDir
	DayKlineDir = filepath.Join(DatabaseDir, "day-kline")
	MinKlineDir = filepath.Join(DatabaseDir, "min-kline")
	Pull        *extend.PullKline
	Manage      *tdx.Manage
)

func init() {

	db, err := xorms.NewSqlite(filepath.Join(DatabaseDir, "update.db"))
	logs.PanicErr(err)

	update, err := tdx.NewUpdated(db, 15, 1)
	logs.PanicErr(err)

	Manage, err = tdx.NewManage(tdx.WithDialGbbqDefault())
	logs.PanicErr(err)

	Pull = extend.NewPullKline(extend.PullKlineConfig{
		Tables:     []string{extend.Day},
		Dir:        DayKlineDir,
		Goroutines: 10,
	})

	key := "pull"
	if updated, err := update.Updated(key); err != nil || !updated {
		err = Pull.Update(Manage)
		logs.PanicErr(err)
		err = update.Update(key)
		logs.PanicErr(err)
	}

}

func main() {
	now := time.Now()

	codes := []string(nil)
	for _, v := range Manage.Codes.GetStockCodes() {
		if strings.HasPrefix(v, "sh60") || strings.HasPrefix(v, "sz000") {
			codes = append(codes, v)
		}
	}

	//codes = codes[:200]

	start := now.AddDate(-5, 0, 0)
	end := now.AddDate(-4, 0, 0)

	ls, err := Backtest(s1{}, codes, start, end)
	logs.PanicErr(err)

	fmt.Printf("回测日期范围: %s - %s\n", start.Format(time.DateOnly), end.Format(time.DateOnly))
	Analyze(ls)
}

func Backtest(s Strategy, codes []string, start, end time.Time) ([]BacktestResp, error) {
	result := make([]BacktestResp, 0, len(codes))
	mu := sync.Mutex{}
	b := bar.NewCoroutine(
		len(codes),
		10,
		bar.WithPrefix("[回测][xx000000]"),
	)
	defer b.Close()
	for _, code := range codes {
		b.Go(func() {
			b.SetPrefix("[回测][" + code + "]")
			resp := BacktestResp{Code: code}
			dks, err := getDayKlines(code, start, end)
			if err != nil {
				b.Logf("[错误] %s", err)
				b.Flush()
				return
			}
			mks, err := getMinKlines(code, start, end)
			if err != nil {
				b.Logf("[错误] %s", err)
				b.Flush()
				return
			}
			resp.Trades = DoStrategy(s, dks, mks)
			mu.Lock()
			defer mu.Unlock()
			result = append(result, resp)
		})

	}
	b.Wait()
	return result, nil
}

func Screen(s Strategy, codes []string) {

}

/*



 */

func DoStrategy(s Strategy, dks extend.Klines, mks protocol.Klines) []Trade {
	mmks := map[string]protocol.Klines{}
	for _, mk := range mks {
		key := mk.Time.Format(time.DateOnly)
		mmks[key] = append(mmks[key], mk)
	}
	ts := []Trade(nil)
	for i, dk := range dks {
		if i+1 >= len(dks) {
			continue
		}
		mk0, ok := mmks[dk.Time.Format(time.DateOnly)]
		if !ok {
			continue
		}
		mk1, ok := mmks[dks[i+1].Time.Format(time.DateOnly)]
		if !ok {
			continue
		}
		if s.Signal(dks[:i+1], mk0) {
			t := Trade{
				Time: dk.Time,
				Buy: func() protocol.Price {
					for _, v := range mk0 {
						//到达买点,按最高价+1分买入,提升成交成功率
						if v.Time.Format(time.TimeOnly) == "14:50:00" {
							return v.High + protocol.Yuan(0.01)
						}
					}
					//否则按最高价,增加容错
					return dk.High
				}(),
				Sell: func() protocol.Price {
					for _, v := range mk1 {
						//到达卖点,按最低价-1分卖出,提升成交成功率
						if v.Time.Format(time.TimeOnly) == "10:00:00" {
							return v.Low - protocol.Yuan(0.01)
						}
					}
					//否则按最低价,增加容错
					return dk.Low
				}(),
			}
			ts = append(ts, t)
		}
	}
	return ts
}

/*



 */

type BacktestResp struct {
	Code   string  //代码
	Trades []Trade //交易记录
}

type Trade struct {
	Time time.Time
	Buy  protocol.Price
	Sell protocol.Price
}

/*




 */

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
