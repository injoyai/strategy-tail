package core

import (
	"strings"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/v2/protocol"
)

func TestReportMACDUsesStrategyConvention(t *testing.T) {
	ks := extend.Klines{}
	for i, close := range []float64{10, 11, 9, 12, 13} {
		ks = append(ks, &extend.Kline{Kline: &protocol.Kline{Time: time.Date(2024, 1, i+1, 0, 0, 0, 0, time.Local), Close: close}})
	}
	values := reportMACD(ks)
	if len(values) != len(ks) || values[1][0] == 0 || values[1][2] == 0 {
		t.Fatal("report indicators must retain history")
	}
	for _, v := range values {
		if diff := (v[0] - v[1]) - v[2]; diff < -1e-12 || diff > 1e-12 {
			t.Fatal("MACD histogram must be DIF−DEA without a multiplier")
		}
	}
}

func TestAllHTMLReportsUseSharedKlineRuntime(t *testing.T) {
	trades, err := buildTradesExportHTML(nil, map[string]any{"strategy": "测试"})
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"trades":  trades,
		"annual":  professionalReportHTML([]byte("[]"), []byte("[]"), []byte("[]"), []byte("[]"), "[]"),
		"forward": futureReportHTML("测试", "[]", "[]", "[]", "[]", "[]", "[]"),
	} {
		if !strings.Contains(content, "StrategyKline.mount") || !strings.Contains(content, "KLineChart v10.1.0") {
			t.Fatalf("%s report lacks embedded KLineChart runtime", name)
		}
		if strings.Contains(content, "function calcMACD") || strings.Contains(content, "type:'candlestick'") || strings.Contains(content, "type: 'candlestick'") {
			t.Fatalf("%s report retains the former K-line implementation", name)
		}
	}
}
