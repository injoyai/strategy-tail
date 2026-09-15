package sell

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

// make尾盘K线 构造 n 根日K，最后一根的时间用 lastTime 指定，
// 模拟引擎分钟级覆写后的快照（Time 为日内分钟时间戳）或无分钟数据退化（00:00:00）。
func make尾盘K线(n int, lastTime time.Time) extend.Klines {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	ks := make(extend.Klines, n)
	for i := 0; i < n; i++ {
		price := 10 + float64(i)*0.1
		t := base.AddDate(0, 0, i)
		if i == n-1 {
			t = lastTime
		}
		ks[i] = &extend.Kline{
			Unix: t.Unix(),
			Kline: &protocol.Kline{
				Time:   t,
				Open:   protocol.Yuan(price),
				Close:  protocol.Yuan(price),
				High:   protocol.Yuan(price + 0.5),
				Low:    protocol.Yuan(price - 0.5),
				Volume: 100,
			},
		}
	}
	return ks
}

// 尾盘时刻（本地 5 分钟线最后一根 14:55，收盘价 ≈ 当日收盘）
var (
	tail1455 = time.Date(2026, 1, 4, 14, 55, 0, 0, time.Local)
	tail1500 = time.Date(2026, 1, 4, 15, 0, 0, 0, time.Local)
	tail1450 = time.Date(2026, 1, 4, 14, 50, 0, 0, time.Local)
	tail0935 = time.Date(2026, 1, 4, 9, 35, 0, 0, time.Local)
	tail0000 = time.Date(2026, 1, 4, 0, 0, 0, 0, time.Local) // 无分钟数据退化
)

func Test持仓N天尾盘_T加1尾盘应卖出(t *testing.T) {
	ks := make尾盘K线(4, tail1455)
	buy := core.Buy{Code: "sh600000", Time: ks[2].Time, Price: ks[2].Close}

	s := A持仓N天尾盘{Days: 1}
	if !s.Sell("sh600000", ks, buy) {
		t.Fatal("T+1 尾盘(14:55)应触发卖出")
	}
}

func Test持仓N天尾盘_T加1收盘标记应卖出(t *testing.T) {
	ks := make尾盘K线(4, tail1500)
	buy := core.Buy{Code: "sh600000", Time: ks[2].Time, Price: ks[2].Close}

	s := A持仓N天尾盘{Days: 1}
	if !s.Sell("sh600000", ks, buy) {
		t.Fatal("T+1 收盘标记(15:00)应触发卖出")
	}
}

func Test持仓N天尾盘_T加1早盘不应卖出(t *testing.T) {
	ks := make尾盘K线(4, tail0935)
	buy := core.Buy{Code: "sh600000", Time: ks[2].Time, Price: ks[2].Close}

	s := A持仓N天尾盘{Days: 1}
	if s.Sell("sh600000", ks, buy) {
		t.Fatal("T+1 早盘(09:35)不应触发卖出")
	}
}

func Test持仓N天尾盘_T加1未到尾盘时间不应卖出(t *testing.T) {
	ks := make尾盘K线(4, tail1450)
	buy := core.Buy{Code: "sh600000", Time: ks[2].Time, Price: ks[2].Close}

	s := A持仓N天尾盘{Days: 1}
	if s.Sell("sh600000", ks, buy) {
		t.Fatal("T+1 14:50 未到尾盘时间不应触发卖出")
	}
}

func Test持仓N天尾盘_买入日不应卖出(t *testing.T) {
	ks := make尾盘K线(3, tail1455)
	ks[2] = &extend.Kline{ // 买入日最后一根还原为日K时间（引擎当日不覆写）
		Unix: time.Date(2026, 1, 3, 0, 0, 0, 0, time.Local).Unix(),
		Kline: &protocol.Kline{
			Time:   time.Date(2026, 1, 3, 0, 0, 0, 0, time.Local),
			Open:   protocol.Yuan(10.2),
			Close:  protocol.Yuan(10.2),
			High:   protocol.Yuan(10.7),
			Low:    protocol.Yuan(9.7),
			Volume: 100,
		},
	}
	buy := core.Buy{Code: "sh600000", Time: ks[2].Time, Price: ks[2].Close}

	s := A持仓N天尾盘{Days: 1}
	if s.Sell("sh600000", ks, buy) {
		t.Fatal("买入日（持仓0天）不应触发卖出")
	}
}

func Test持仓N天尾盘_无分钟数据退化应卖出(t *testing.T) {
	// 引擎无分钟数据时快照为单根日K，时间 00:00:00，成交价即当日收盘
	ks := make尾盘K线(4, tail0000)
	buy := core.Buy{Code: "sh600000", Time: ks[2].Time, Price: ks[2].Close}

	s := A持仓N天尾盘{Days: 1}
	if !s.Sell("sh600000", ks, buy) {
		t.Fatal("无分钟数据退化(00:00:00)应视为收盘卖出")
	}
}

func Test持仓N天尾盘_Days为2时T加1不应卖出(t *testing.T) {
	ks := make尾盘K线(4, tail1455)
	buy := core.Buy{Code: "sh600000", Time: ks[2].Time, Price: ks[2].Close}

	s := A持仓N天尾盘{Days: 2}
	if s.Sell("sh600000", ks, buy) {
		t.Fatal("Days=2 时 T+1 尾盘不应触发卖出")
	}
}

func Test持仓N天尾盘_Days为2时T加2尾盘应卖出(t *testing.T) {
	ks := make尾盘K线(5, tail1455)
	buy := core.Buy{Code: "sh600000", Time: ks[2].Time, Price: ks[2].Close}

	s := A持仓N天尾盘{Days: 2}
	if !s.Sell("sh600000", ks, buy) {
		t.Fatal("Days=2 时 T+2 尾盘应触发卖出")
	}
}

func Test持仓N天尾盘_空数据不应卖出(t *testing.T) {
	s := A持仓N天尾盘{Days: 1}
	if s.Sell("sh600000", extend.Klines{}, core.Buy{}) {
		t.Fatal("空K线数据不应触发卖出")
	}
}

func Test持仓N天尾盘_默认值(t *testing.T) {
	s := A持仓N天尾盘{}
	if s.Name() == "" {
		t.Fatal("Name 不应为空")
	}

	// 默认 Days=1、Time=14:55:00：T+1 尾盘应触发，T+1 早盘不应触发
	ks := make尾盘K线(4, tail1455)
	buy := core.Buy{Code: "sh600000", Time: ks[2].Time, Price: ks[2].Close}
	if !s.Sell("sh600000", ks, buy) {
		t.Fatal("默认配置下 T+1 尾盘应触发卖出")
	}

	ks2 := make尾盘K线(4, tail0935)
	if s.Sell("sh600000", ks2, core.Buy{Code: "sh600000", Time: ks2[2].Time, Price: ks2[2].Close}) {
		t.Fatal("默认配置下 T+1 早盘不应触发卖出")
	}
}
