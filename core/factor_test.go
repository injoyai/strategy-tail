package core

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

// stubFactor 测试桩：恒返回构造时设定的值。
type stubFactor struct {
	name string
	val  float64
}

func (f stubFactor) Name() string                                 { return f.name }
func (f stubFactor) Value(code string, dks extend.Klines) float64 { return f.val }

func TestDayOf_截断到当日墙钟零点(t *testing.T) {
	// 带时分秒的时间应截断到同日 00:00:00（不能按 UTC 绝对时间 Truncate）
	in := time.Date(2025, 3, 5, 15, 30, 0, 0, time.Local)
	got := DayOf(in)
	want := time.Date(2025, 3, 5, 0, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Fatalf("DayOf=%v want %v", got, want)
	}
}

// mkKline 构造单根K线（因子全是比值计算，标度无关）。
func mkKline(base time.Time, i int, close float64) *extend.Kline {
	tm := base.AddDate(0, 0, i)
	return &extend.Kline{
		Unix: tm.Unix(),
		Kline: &protocol.Kline{
			Time:   tm,
			Open:   protocol.Yuan(close),
			Close:  protocol.Yuan(close),
			High:   protocol.Yuan(close),
			Low:    protocol.Yuan(close),
			Volume: 10000,
		},
	}
}
