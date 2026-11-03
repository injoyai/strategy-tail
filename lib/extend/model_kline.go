package extend

import (
	"math"

	"github.com/injoyai/tdx/v2/protocol"
)

type Info struct {
	Code       string  `json:"code"`       //代码
	Name       string  `json:"name"`       //名称
	Price      float64 `json:"price"`      //最新价
	Turnover   float64 `json:"turnover"`   //换手率
	FloatStock int64   `json:"floatStock"` //流通股本
	TotalStock int64   `json:"totalStock"` //总股本
	FloatValue float64 `json:"floatValue"` //流通市值
	TotalValue float64 `json:"totalValue"` //总市值
}

type Kline struct {
	Unix            int64 `xorm:"pk"`
	*protocol.Kline `xorm:"extends"`
	Turnover        float64
	FloatStock      int64
	TotalStock      int64
	//InsideDish      int64
	//OuterDisc       int64
}

func (this *Kline) FloatValue() float64 {
	return this.Close * float64(this.FloatStock)
}

func (this *Kline) TotalValue() float64 {
	return this.Close * float64(this.TotalStock)
}

/*



 */

type Klines []*Kline

// REF 前n天
func (this Klines) REF(n int) *Kline {
	return this[len(this)-n-1]
}

// HHV 近n天的最高价,同tdx公式命名
func (this Klines) HHV(n int) float64 {
	p := 0.0
	for i := len(this) - n; i < len(this); i++ {
		if p < this[i].High {
			p = this[i].High
		}
	}
	return p
}

// LLV 近n天的最低价,同tdx公式命名
func (this Klines) LLV(n int) float64 {
	p := 0.0
	for i := len(this) - n; i < len(this); i++ {
		if p == 0.0 || p > this[i].Low {
			p = this[i].Low
		}
	}
	return p
}

// MA 均线
func (ks Klines) MA(n int) float64 {
	if len(ks) < n {
		return 0.0
	}
	sum := 0.0
	// 取最后n个
	for _, k := range ks[len(ks)-n:] {
		sum += k.Close
	}
	return sum / float64(n)
}

// EMA MACD的基础
func (ks Klines) EMA(n int) float64 {
	if len(ks) == 0 || n <= 0 {
		return 0.0
	}

	ema := ks[0].Close
	den := float64(n + 1)
	num := float64(2)

	for i := 1; i < len(ks); i++ {
		ema = float64((ks[i].Close*num + ema*(den-num)) / den)

	}
	return ema
}

// MACD 常用于短线核心
func (ks Klines) MACD() (dif, dea, hist float64) {
	if len(ks) == 0 {
		return 0.0, 0.0, 0.0
	}

	ema12 := ks[0].Close
	ema26 := ks[0].Close
	den12 := float64(13)
	den26 := float64(27)
	denDea := float64(10)
	num := float64(2)

	for i := 1; i < len(ks); i++ {
		ema12 = float64((ks[i].Close*num + ema12*(den12-num)) / den12)
		ema26 = float64((ks[i].Close*num + ema26*(den26-num)) / den26)
		dif = ema12 - ema26
		dea = float64((dif*num + dea*(denDea-num)) / denDea)
		hist = (dif - dea) * 2
	}
	return dif, dea, hist
}

// RSI 常用于超买超卖
func (ks Klines) RSI(n int) int64 {
	if len(ks) == 0 || n <= 0 {
		return 0
	}
	var gain, loss float64
	var rsi int64

	for i := 1; i < len(ks); i++ {
		diff := ks[i].Close - ks[i-1].Close

		if diff > 0 {
			gain += diff
		} else {
			loss -= diff
		}

		if i >= n+1 {
			prev := ks[i-n].Close - ks[i-n-1].Close
			if prev > 0 {
				gain -= prev
			} else {
				loss += prev
			}
		}

		if i >= n && loss > 0 {
			rsi = int64(100 * gain / (gain + loss))
		}
	}
	return rsi
}

// BOLL 布林带（洗盘神器）
func (ks Klines) BOLL(n int) (upper, mid, lower float64) {
	if len(ks) < n || n <= 0 {
		return 0.0, 0.0, 0.0
	}

	mid = ks.MA(n)
	var sum float64
	for _, k := range ks[len(ks)-n:] {
		d := k.Close - mid
		sum += d * d
	}
	std := math.Sqrt(sum / float64(n))
	upper = mid + std*2
	lower = mid - std*2
	return upper, mid, lower
}

// ATR 常用于判断是否该止损
func (ks Klines) ATR(n int) float64 {
	if len(ks) == 0 || n <= 0 {
		return 0.0
	}
	var sum float64
	var atr float64

	for i := 1; i < len(ks); i++ {
		h := ks[i].High
		l := ks[i].Low
		pc := ks[i-1].Close

		tr := max(h-l, max(math.Abs(h-pc), math.Abs(l-pc)))
		sum += tr

		if i >= n {
			prev := max(ks[i-n+1].High-ks[i-n+1].Low,
				max(math.Abs(ks[i-n+1].High-ks[i-n].Close), math.Abs(ks[i-n+1].Low-ks[i-n].Close)))
			sum -= prev
			atr = sum / float64(n)
		}
	}
	return atr
}

func (ks Klines) VWAP() float64 {
	if len(ks) == 0 {
		return 0.0
	}
	var volSum int64
	var amtSum float64
	var vwap float64

	for i := 0; i < len(ks); i++ {
		volSum += ks[i].Volume
		amtSum += ks[i].Amount
		if volSum > 0 {
			vwap = amtSum / float64(volSum)
		}
	}
	return vwap
}
