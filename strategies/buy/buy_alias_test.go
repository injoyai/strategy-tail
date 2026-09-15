package buy

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

func Test策略旧名保持兼容(t *testing.T) {
	oldMA := BuyCloseAboveMA{Period: 5}
	var oldShrink VolumeShrink = A缩量{Days: 1, Ratio: 0.8}
	if oldMA.Name() != "收盘高于5日均线" {
		t.Fatalf("MA 旧名行为变化: %s", oldMA.Name())
	}
	if oldShrink.Name() != "缩量" {
		t.Fatalf("缩量旧名行为变化: %s", oldShrink.Name())
	}
	if got := (BuyCloseAboveMA{}).Name(); got != "收盘高于0日均线" {
		t.Fatalf("兼容包装器的零值 Name() = %q", got)
	}
}

func TestA缩量(t *testing.T) {
	dks := aliasTestKlines(100, 70)
	if !(&A缩量{Days: 1, Ratio: 0.8}).Buy("test", dks) {
		t.Fatal("成交量 70 应低于昨日成交量 100 的 80%")
	}
	if (&A缩量{Days: 1, Ratio: 0.6}).Buy("test", dks) {
		t.Fatal("成交量 70 不应低于昨日成交量 100 的 60%")
	}
}

func aliasTestKlines(volumes ...int64) extend.Klines {
	result := make(extend.Klines, len(volumes))
	for i, volume := range volumes {
		at := time.Date(2026, 1, i+1, 0, 0, 0, 0, time.Local)
		result[i] = &extend.Kline{Kline: &protocol.Kline{
			Time: at, Open: protocol.Yuan(10), High: protocol.Yuan(10),
			Low: protocol.Yuan(10), Close: protocol.Yuan(10), Volume: volume,
		}}
	}
	return result
}
