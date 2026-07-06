package core

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

// auditKline 构造一根日线（Open=Close=close，给定 Low/High）。
func auditKline(t time.Time, low, high, close float64) *extend.Kline {
	return &extend.Kline{
		Unix: t.Unix(),
		Kline: &protocol.Kline{
			Time:  t,
			Open:  protocol.Yuan(close),
			High:  protocol.Yuan(high),
			Low:   protocol.Yuan(low),
			Close: protocol.Yuan(close),
		},
	}
}

func TestAuditLookAhead_盘内卖出价不等于收盘价但在区间内不报错(t *testing.T) {
	// 当日 Low=23.0 High=24.0 Close=23.69
	day := auditKline(time.Date(2026, 7, 6, 15, 0, 0, 0, time.Local), 23.0, 24.0, 23.69)
	getDks := func(code string) (extend.Klines, error) {
		return extend.Klines{day}, nil
	}
	cost := Cost{Slippage: protocol.Yuan(0.01)}
	// 盘内卖出：原始分钟收盘 23.50（在 [Low,High] 内），SellExec=23.50-0.01=23.49
	// 买入：close+slip=23.70
	tr := Trade{
		Code:          "sh601233",
		BuyTime:       day.Time,
		SellTime:      day.Time,
		BuyExecPrice:  protocol.Yuan(23.70),
		SellExecPrice: protocol.Yuan(23.49),
	}
	res := AuditLookAhead([]Trade{tr}, cost, getDks)
	if !res.Passed {
		t.Fatalf("盘内卖出价在区间内不应报前视偏差, got issues: %v", res.Issues)
	}
}

func TestAuditLookAhead_滑点边界成交价不报错(t *testing.T) {
	// close==High（收在最高），buy exec = High+slip，sell exec = close-slip
	day := auditKline(time.Date(2026, 7, 6, 15, 0, 0, 0, time.Local), 23.0, 23.69, 23.69)
	getDks := func(code string) (extend.Klines, error) {
		return extend.Klines{day}, nil
	}
	cost := Cost{Slippage: protocol.Yuan(0.01)}
	tr := Trade{
		Code:          "sh601233",
		BuyTime:       day.Time,
		SellTime:      day.Time,
		BuyExecPrice:  protocol.Yuan(23.70), // 23.69 + 0.01
		SellExecPrice: protocol.Yuan(23.68), // 23.69 - 0.01
	}
	res := AuditLookAhead([]Trade{tr}, cost, getDks)
	if !res.Passed {
		t.Fatalf("滑点导致的边界成交价不应报错, got %v", res.Issues)
	}
}

func TestAuditLookAhead_卖出价超出日线区间应报错(t *testing.T) {
	day := auditKline(time.Date(2026, 7, 6, 15, 0, 0, 0, time.Local), 23.0, 24.0, 23.69)
	getDks := func(code string) (extend.Klines, error) {
		return extend.Klines{day}, nil
	}
	cost := Cost{Slippage: protocol.Yuan(0.01)}
	// 前视偏差：卖出成交价 25.00 远超当日 High+slip=24.01
	tr := Trade{
		Code:          "sh601233",
		BuyTime:       day.Time,
		SellTime:      day.Time,
		BuyExecPrice:  protocol.Yuan(23.70),
		SellExecPrice: protocol.Yuan(25.00),
	}
	res := AuditLookAhead([]Trade{tr}, cost, getDks)
	if res.Passed {
		t.Fatal("卖出价超出区间应报前视偏差")
	}
}

func TestAuditLookAhead_买入价超出日线区间应报错(t *testing.T) {
	day := auditKline(time.Date(2026, 7, 6, 15, 0, 0, 0, time.Local), 23.0, 24.0, 23.69)
	getDks := func(code string) (extend.Klines, error) {
		return extend.Klines{day}, nil
	}
	cost := Cost{Slippage: protocol.Yuan(0.01)}
	// 买入成交价 22.00 低于当日 Low-slip=22.99
	tr := Trade{
		Code:          "sh601233",
		BuyTime:       day.Time,
		SellTime:      day.Time,
		BuyExecPrice:  protocol.Yuan(22.00),
		SellExecPrice: protocol.Yuan(23.68),
	}
	res := AuditLookAhead([]Trade{tr}, cost, getDks)
	if res.Passed {
		t.Fatal("买入价超出区间应报前视偏差")
	}
}

func TestAuditLookAhead_时间倒置应报错(t *testing.T) {
	getDks := func(code string) (extend.Klines, error) {
		return extend.Klines{auditKline(time.Date(2026, 7, 6, 15, 0, 0, 0, time.Local), 23, 24, 23.69)}, nil
	}
	tr := Trade{
		Code:     "sh601233",
		BuyTime:  time.Date(2026, 7, 8, 15, 0, 0, 0, time.Local),
		SellTime: time.Date(2026, 7, 6, 15, 0, 0, 0, time.Local),
	}
	res := AuditLookAhead([]Trade{tr}, Cost{}, getDks)
	if res.Passed {
		t.Fatal("时间倒置应报错")
	}
}

func TestAuditLookAhead_卖出日期不存在应报错(t *testing.T) {
	getDks := func(code string) (extend.Klines, error) {
		return extend.Klines{auditKline(time.Date(2026, 7, 6, 15, 0, 0, 0, time.Local), 23, 24, 23.69)}, nil
	}
	tr := Trade{
		Code:          "sh601233",
		BuyTime:       time.Date(2026, 7, 6, 15, 0, 0, 0, time.Local),
		SellTime:      time.Date(2026, 7, 9, 15, 0, 0, 0, time.Local), // 不在K线
		BuyExecPrice:  protocol.Yuan(23.70),
		SellExecPrice: protocol.Yuan(23.50),
	}
	res := AuditLookAhead([]Trade{tr}, Cost{}, getDks)
	if res.Passed {
		t.Fatal("卖出日期不存在应报错")
	}
}
