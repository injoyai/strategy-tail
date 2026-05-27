package core

import (
	"fmt"
	"sync"
	"time"

	"github.com/injoyai/bar"
	"github.com/injoyai/logs"
	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

type Backtest struct {
	Buyer
	Seller
	Goroutines   int                                                              //协程数量
	Codes        []string                                                         //股票代码
	Years        []int                                                            //回测年
	GetDayKlines func(code string, start, end time.Time) (extend.Klines, error)   //获取日线数据函数
	GetMinKlines func(code string, start, end time.Time) (protocol.Klines, error) //获取分钟数据函数
	UseMinute    bool                                                             //使用分钟数据

	Slippage       protocol.Price //滑点(单边,按每股绝对价格加减)
	CommissionRate float64        //手续费率(买/卖都收,例如 0.0003)
	StampDutyRate  float64        //印花税率(仅卖出,例如 0.001)
}

func (this Backtest) Run() {
	logs.Info(this.Buyer.Name() + " 买入")
	logs.Info(this.Seller.Name() + " 卖出")

	results := make([]AnalyzeResult, 0, len(this.Years))
	for _, year := range this.Years {

		ls, err := this._backtest(this.Codes, year)
		logs.PanicErr(err)

		result := Analyze(year, ls, func(code string) (extend.Klines, error) {
			return this.GetDayKlines(code, time.Time{}, time.Now())
		})
		results = append(results, result)
	}
	PrintAnalyzeResults(results)
}

func (this Backtest) _backtest(codes []string, year int) ([]Trade, error) {

	hisStart := time.Date(year-2, 6, 1, 0, 0, 0, 0, time.Local)
	start := time.Date(year, 1, 1, 0, 0, 0, 0, time.Local)
	end := time.Date(year, 12, 31, 23, 0, 0, 0, time.Local)

	result := []Trade(nil)
	mu := sync.Mutex{}
	b := bar.NewCoroutine(
		len(codes),
		this.Goroutines,
		bar.WithPrefix(fmt.Sprintf("[%d][%s]", year, "xx000000")),
	)
	defer b.Close()
	for _, code := range codes {
		b.Go(func() {
			b.SetPrefix(fmt.Sprintf("[%d][%s]", year, code))

			//获取历史数据,多取一点
			dks, err := this.GetDayKlines(code, hisStart, end)
			if err != nil {
				b.Logf("[错误] %s", err)
				b.Flush()
				return
			}

			//提取不需要回测的历史数据,用于计算指标
			his := []*extend.Kline(nil)
			for i, v := range dks {
				if v.Time.Before(start) {
					his = append(his, v)
				} else {
					dks = dks[i:]
					break
				}
			}

			//获取历史分钟数据
			var mks protocol.Klines
			if this.GetMinKlines != nil {
				mks, err = this.GetMinKlines(code, start, end)
				if err != nil {
					b.Logf("[错误] %s", err)
					b.Flush()
					return
				}
			}

			//执行策略
			ts := this.Do(code, his, dks, mks)
			mu.Lock()
			defer mu.Unlock()
			result = append(result, ts...)
		})

	}
	b.Wait()
	return result, nil
}

func (this Backtest) Do(code string, his, dks extend.Klines, mks protocol.Klines) []Trade {

	m := map[string]protocol.Klines{}
	for _, mk := range mks {
		key := mk.Time.Format(time.DateOnly)
		m[key] = append(m[key], mk)
	}

	ts := []Trade(nil)

	var currentBuy *Buy
	for i := 0; i < len(dks); i++ {

		today := dks[i]

		_his := append(his, dks[:i]...)

		if currentBuy == nil {
			ls := append(_his, today)
			if this.Buy(code, ls) {
				currentBuy = &Buy{
					Code:  code,
					Time:  today.Time,
					Price: today.Close,
				}
			}

		} else {

			todayMinuteKlines, ok := m[today.Time.Format(time.DateOnly)]
			if !ok || len(todayMinuteKlines) == 0 {
				todayMinuteKlines = protocol.Klines{today.Kline}
			}

			for ii := range todayMinuteKlines {
				today.Kline = todayMinuteKlines[:ii].Kline(todayMinuteKlines[0].Time, todayMinuteKlines[0].Open)

				ls := append(_his, today)

				if this.Sell(code, ls, *currentBuy) {
					slippage := this.Slippage
					if slippage == 0 {
						slippage = protocol.Yuan(0.01)
					}

					buyExecPrice := currentBuy.Price + slippage
					sellExecPrice := today.Close - slippage

					buyFee := protocol.Yuan(buyExecPrice.Float64() * this.CommissionRate)
					sellFee := protocol.Yuan(sellExecPrice.Float64() * (this.CommissionRate + this.StampDutyRate))

					tr := Trade{
						Code:      code,
						BuyTime:   currentBuy.Time,
						SellTime:  today.Time,
						BuyPrice:  buyExecPrice + buyFee,
						SellPrice: sellExecPrice - sellFee,
					}
					ts = append(ts, tr)
					currentBuy = nil
					break
				}

			}

		}

	}
	return ts
}
