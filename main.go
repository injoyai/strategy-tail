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
		if Manage.Workday.TodayIs() {
			err = Pull.Update(Manage)
			logs.PanicErr(err)
			err = update.Update(key)
			logs.PanicErr(err)
		}
	}

}

func main() {

	codes := []string(nil)
	for _, v := range Manage.Codes.GetStockCodes() {
		if strings.HasPrefix(v, "sh60") || strings.HasPrefix(v, "sz00") {
			codes = append(codes, v)
		}
	}

	s := StrategyVolume{
		BuyTime:  "14:40:00",
		SellTime: "10:00:00",
	}

	//screen(s, codes)

	years := []int{2018, 2019, 2020, 2021, 2022, 2023, 2024, 2025}
	years = []int{2026}
	backtest(s, codes, years)
}

func screen(s Strategy, codes []string) {

	var err error
	//err := Pull.Update(Manage)
	//logs.PanicErr(err)

	now := time.Now().Add(-time.Hour * 24 * 7)
	end := time.Date(now.Year(), now.Month(), now.Day(), 15, 1, 0, 0, time.Local)

	//end = time.Date(2026, 1, 15, 15, 0, 0, 0, time.Local)
	//codes = []string{"sz002830"}

	fmt.Println("[时间]", end.Format(time.DateOnly))
	err = Screen(s, codes, end.AddDate(0, -4, 0), end)
	logs.PanicErr(err)

}

func backtest(s Strategy, codes []string, years []int) {

	for _, year := range years {
		start := time.Date(year, 1, 1, 0, 0, 0, 0, time.Local)
		end := time.Date(year, 12, 31, 23, 0, 0, 0, time.Local)

		ls, err := Backtest(s, codes, start, end)
		logs.PanicErr(err)

		fmt.Printf("回测年份: %d\n", year)
		Analyze(ls)
	}
}

func Backtest(s Strategy, codes []string, start, end time.Time) ([]Trade, error) {
	result := []Trade(nil)
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
			dks, err := getDayKlines(code, start, end)
			if err != nil {
				b.Logf("[错误] %s", err)
				b.Flush()
				return
			}
			var mks protocol.Klines
			mks, err = getMinKlines(code, start, end)
			if err != nil {
				b.Logf("[错误] %s", err)
				b.Flush()
				return
			}
			ts := DoStrategy(s, code, dks, mks)
			mu.Lock()
			defer mu.Unlock()
			result = append(result, ts...)
		})

	}
	b.Wait()
	return result, nil
}

func Screen(s Strategy, codes []string, start, end time.Time) error {
	b := bar.NewCoroutine(
		len(codes),
		10,
		bar.WithPrefix("[选股][xx000000]"),
	)
	for _, code := range codes {
		b.Go(func() {
			b.SetPrefix("[选股][" + code + "]")
			dks, err := getDayKlines(code, start, end)
			if err != nil {
				b.Logf("[错误] %s", err)
				b.Flush()
				return
			}
			//mks, err := getMinKlines(code, start, end)
			//if err != nil {
			//	b.Logf("[错误] %s", err)
			//	b.Flush()
			//	return
			//}
			buy := DoStrategyBuy(s, code, dks, nil)
			if buy == nil {
				return
			}
			b.Logf("[选股] %s %s %f", buy.Code, buy.Time.Format(time.DateTime), buy.Price.Float64())
			b.Flush()
		})

	}
	b.Wait()
	return nil
}

/*



 */

func DoStrategyBuy(s Strategy, code string, dks extend.Klines, mks protocol.Klines) *Buy {
	buy := s.Buy(code, dks, nil)
	if buy != nil {
		return &Buy{
			Code:  code,
			Time:  buy.Time,
			Price: buy.Price,
		}
	}
	return nil
}

func DoStrategy(s Strategy, code string, dks extend.Klines, mks protocol.Klines) []Trade {
	mmks := map[string]protocol.Klines{}
	for _, mk := range mks {
		key := mk.Time.Format(time.DateOnly)
		mmks[key] = append(mmks[key], mk)
	}
	ts := []Trade(nil)

	var currentBuy *Buy
	var buyIndex int

	for i := 0; i < len(dks); i++ {
		dk := dks[i]
		mk, ok := mmks[dk.Time.Format(time.DateOnly)]
		if !ok {
			//continue
		}

		if currentBuy == nil {
			// 尝试买入
			buy := s.Buy(code, dks[:i+1], mk)
			if buy != nil {
				currentBuy = buy
				buyIndex = i
			}
		} else {
			// 持仓中，检查卖出条件
			// 必须是 T+1 之后 (i > buyIndex)
			if i > buyIndex {
				sell := s.Sell(code, dks[:i+1], mk, *currentBuy)
				// 这里我们可以传入持仓天数，或者让 Sell 方法自己判断
				// 为了简单，我们假定 Sell 方法决定是否卖出

				// 强制最大持仓天数兜底，例如 20 天，防止无限持仓 (可选，用户没说先不加，完全交给策略)
				// if i - buyIndex > 20 { ... }

				if sell != nil {
					tr := Trade{
						Code:      code,
						BuyTime:   currentBuy.Time,
						SellTime:  sell.Time,
						BuyPrice:  currentBuy.Price + protocol.Yuan(0.01),
						SellPrice: sell.Price - protocol.Yuan(0.01),
					}
					ts = append(ts, tr)
					currentBuy = nil
					buyIndex = 0
				}
			}
		}
	}
	return ts
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
