package common

import (
	"testing"
	"time"

	"github.com/injoyai/tdx/protocol"
)

// 红利测试基准时间 固定时刻，避免依赖真实当前时间。
var 红利测试基准时间 = time.Date(2026, 9, 19, 12, 0, 0, 0, time.Local)

func Test连续分红(t *testing.T) {
	// 分红事件 构造指定年份（每年6月30日除息）的分红事件列表。
	分红事件 := func(years ...int) protocol.XRXDs {
		events := protocol.XRXDs(nil)
		for _, y := range years {
			events = append(events, &protocol.XRXD{
				Code:    "sh600000",
				Time:    time.Date(y, 6, 30, 0, 0, 0, 0, time.Local),
				Fenhong: 5.0,
			})
		}
		return events
	}

	cases := []struct {
		name   string
		events protocol.XRXDs
		years  int
		want   bool
	}{
		{name: "连续5年分红", events: 分红事件(2021, 2022, 2023, 2024, 2025), years: 5, want: true},
		{name: "超过5年分红", events: 分红事件(2018, 2019, 2020, 2021, 2022, 2023, 2024, 2025), years: 5, want: true},
		{name: "中间断档一年", events: 分红事件(2021, 2022, 2023, 2025), years: 5, want: false},
		{name: "仅有4年", events: 分红事件(2022, 2023, 2024, 2025), years: 5, want: false},
		{name: "无任何分红", events: nil, years: 5, want: false},
		{name: "仅当年分红不算", events: 分红事件(2026), years: 5, want: false},
		{name: "当年加连续5年", events: 分红事件(2021, 2022, 2023, 2024, 2025, 2026), years: 5, want: true},
		{name: "送转股不计入分红", events: func() protocol.XRXDs {
			return protocol.XRXDs{
				{Code: "sh600000", Time: time.Date(2021, 6, 30, 0, 0, 0, 0, time.Local), Songzhuangu: 5},
				{Code: "sh600000", Time: time.Date(2022, 6, 30, 0, 0, 0, 0, time.Local), Songzhuangu: 5},
				{Code: "sh600000", Time: time.Date(2023, 6, 30, 0, 0, 0, 0, time.Local), Songzhuangu: 5},
				{Code: "sh600000", Time: time.Date(2024, 6, 30, 0, 0, 0, 0, time.Local), Songzhuangu: 5},
				{Code: "sh600000", Time: time.Date(2025, 6, 30, 0, 0, 0, 0, time.Local), Songzhuangu: 5},
			}
		}(), years: 5, want: false},
		{name: "0年要求恒真", events: nil, years: 0, want: true},
	}
	for _, v := range cases {
		t.Run(v.name, func(t *testing.T) {
			if got := 连续分红(v.events, 红利测试基准时间, v.years); got != v.want {
				t.Errorf("连续分红(%d年) = %v, 期望 %v", v.years, got, v.want)
			}
		})
	}
}

func Test近一年每股分红(t *testing.T) {
	// 分红事件 构造指定日期与每10股分红金额的事件。
	分红事件 := func(fenhong float64, t time.Time) *protocol.XRXD {
		return &protocol.XRXD{Code: "sh600000", Time: t, Fenhong: fenhong}
	}

	cases := []struct {
		name   string
		events protocol.XRXDs
		want   float64
	}{
		{
			name:   "无事件",
			events: nil,
			want:   0,
		},
		{
			name: "窗口内一次分红 10派5",
			events: protocol.XRXDs{
				分红事件(5.0, time.Date(2026, 6, 30, 0, 0, 0, 0, time.Local)),
			},
			want: 0.5,
		},
		{
			name: "跨年两次分红合计 10派5+10派6",
			events: protocol.XRXDs{
				分红事件(5.0, time.Date(2025, 10, 10, 0, 0, 0, 0, time.Local)),
				分红事件(6.0, time.Date(2026, 6, 30, 0, 0, 0, 0, time.Local)),
			},
			want: 1.1,
		},
		{
			name: "窗口外的旧分红不计入",
			events: protocol.XRXDs{
				分红事件(5.0, time.Date(2026, 3, 1, 0, 0, 0, 0, time.Local)),
				分红事件(10.0, time.Date(2025, 6, 1, 0, 0, 0, 0, time.Local)), // 早于窗口起点 2025-09-19
			},
			want: 0.5,
		},
		{
			name: "未来事件不计入",
			events: protocol.XRXDs{
				分红事件(5.0, time.Date(2026, 6, 30, 0, 0, 0, 0, time.Local)),
				分红事件(10.0, time.Date(2026, 12, 1, 0, 0, 0, 0, time.Local)),
			},
			want: 0.5,
		},
		{
			name: "送转股不计入",
			events: protocol.XRXDs{
				{Code: "sh600000", Time: time.Date(2026, 6, 30, 0, 0, 0, 0, time.Local), Songzhuangu: 10},
			},
			want: 0,
		},
		{
			name: "窗口起点边界值恰好计入",
			events: protocol.XRXDs{
				// 起点 = 2025-09-19 12:00:00，同刻事件不早于起点，应计入
				分红事件(5.0, 红利测试基准时间.AddDate(-1, 0, 0)),
			},
			want: 0.5,
		},
	}
	for _, v := range cases {
		t.Run(v.name, func(t *testing.T) {
			got := 近一年每股分红(v.events, 红利测试基准时间)
			if diff := got - v.want; diff > 1e-9 || diff < -1e-9 {
				t.Errorf("近一年每股分红 = %v, 期望 %v", got, v.want)
			}
		})
	}
}
