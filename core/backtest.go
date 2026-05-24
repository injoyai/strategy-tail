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
	BuyAll
	SellAny
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
	logs.Info(this.BuyAll.Name() + "买入")
	logs.Info(this.SellAny.Name() + "卖出")

	for _, year := range this.Years {
		start := time.Date(year, 1, 1, 0, 0, 0, 0, time.Local)
		end := time.Date(year, 12, 31, 23, 0, 0, 0, time.Local)

		ls, err := this._backtest(this.Codes, start, end)
		logs.PanicErr(err)

		fmt.Printf("回测年份: %d\n", year)
		Analyze(year, ls, func(code string) (extend.Klines, error) {
			return this.GetDayKlines(code, time.Time{}, time.Now())
		})
	}
}

func (this Backtest) _backtest(codes []string, start, end time.Time) ([]Trade, error) {
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
			dks, err := this.GetDayKlines(code, start, end)
			if err != nil {
				b.Logf("[错误] %s", err)
				b.Flush()
				return
			}
			var mks protocol.Klines
			mks, err = this.GetMinKlines(code, start, end)
			if err != nil {
				b.Logf("[错误] %s", err)
				b.Flush()
				return
			}
			ts := this.Do(code, dks, mks)
			mu.Lock()
			defer mu.Unlock()
			result = append(result, ts...)
		})

	}
	b.Wait()
	return result, nil
}

func (this Backtest) Do(code string, dks extend.Klines, mks protocol.Klines) []Trade {

	m := map[string]protocol.Klines{}
	for _, mk := range mks {
		key := mk.Time.Format(time.DateOnly)
		m[key] = append(m[key], mk)
	}

	ts := []Trade(nil)

	var currentBuy *Buy
	for i := 0; i < len(dks); i++ {

		today := dks[i]

		if currentBuy == nil {
			if this.Buy(code, dks[:i+1]) {
				currentBuy = &Buy{
					Code:  code,
					Time:  today.Time,
					Price: today.Close,
				}
			}
		} else {
			if this.Sell(code, dks[:i+1], *currentBuy) {
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
			}
		}
	}
	return ts
}
