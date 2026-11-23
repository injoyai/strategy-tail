package builder

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/tdx/v2/protocol"
)

// indexTestBars 生成从 start 起 n 根逐日上涨的日线。
func indexTestBars(start time.Time, n int) extend.Klines {
	bars := make(extend.Klines, 0, n)
	for i := 0; i < n; i++ {
		at := start.AddDate(0, 0, i)
		close := 100 + float64(i)
		bars = append(bars, &extend.Kline{Kline: &protocol.Kline{
			Time: at, Open: close, Close: close, High: close, Low: close,
		}})
	}
	return bars
}

// TestIndexBullLazyLoadMatchesDirect 指数条件懒加载的数据与直接构造一致，
// 且同一 Buyer 只读取一次指数日线。
func TestIndexBullLazyLoadMatchesDirect(t *testing.T) {
	indexBars := indexTestBars(time.Date(2024, 1, 1, 0, 0, 0, 0, time.Local), 600)
	stockBars := indexTestBars(time.Date(2024, 9, 1, 0, 0, 0, 0, time.Local), 40)
	calls := 0
	var gotCode string
	SetIndexLoader(func(code string, start, end time.Time) (extend.Klines, error) {
		calls++
		gotCode = code
		return indexBars, nil
	})
	t.Cleanup(func() { SetIndexLoader(nil) })

	buyer, err := (&BuilderNode{Type: "condition", ComponentID: "buy_index_bull", Params: map[string]any{
		"indexCode": "sh000001",
		"periods":   []any{5.0, 10.0},
	}}).Buyer()
	if err != nil {
		t.Fatal(err)
	}
	direct := sb.A指数多头排列{Ks: indexBars, Periods: []int{5, 10}}
	for end := 1; end <= len(stockBars); end++ {
		want := direct.Buy("sh600000", stockBars[:end])
		if got := buyer.Buy("sh600000", stockBars[:end]); got != want {
			t.Fatalf("end=%d 结果不一致: got=%v want=%v", end, got, want)
		}
	}
	if calls != 1 {
		t.Fatalf("指数日线应只加载一次，实际 %d 次", calls)
	}
	if gotCode != "sh000001" {
		t.Fatalf("指数代码错误: %s", gotCode)
	}
}

// TestIndexAboveMAMatchesDirect 指数站上MA条件的懒加载结果与直接构造一致。
func TestIndexAboveMAMatchesDirect(t *testing.T) {
	indexBars := indexTestBars(time.Date(2024, 1, 1, 0, 0, 0, 0, time.Local), 400)
	stockBars := indexTestBars(time.Date(2024, 9, 1, 0, 0, 0, 0, time.Local), 20)
	SetIndexLoader(func(string, time.Time, time.Time) (extend.Klines, error) {
		return indexBars, nil
	})
	t.Cleanup(func() { SetIndexLoader(nil) })

	buyer, err := (&BuilderNode{Type: "condition", ComponentID: "buy_index_above_ma", Params: map[string]any{
		"indexCode": "sh000300",
		"period":    20.0,
	}}).Buyer()
	if err != nil {
		t.Fatal(err)
	}
	direct := sb.A指数站上MA{Ks: indexBars, Period: 20}
	for end := 1; end <= len(stockBars); end++ {
		want := direct.Buy("sh600000", stockBars[:end])
		if got := buyer.Buy("sh600000", stockBars[:end]); got != want {
			t.Fatalf("end=%d 结果不一致: got=%v want=%v", end, got, want)
		}
	}
}

// TestIndexWithoutLoaderNeverBuys 未注入行情时指数条件保守返回 false。
func TestIndexWithoutLoaderNeverBuys(t *testing.T) {
	SetIndexLoader(nil)
	t.Cleanup(func() { SetIndexLoader(nil) })

	buyer, err := (&BuilderNode{Type: "condition", ComponentID: "buy_index_bull"}).Buyer()
	if err != nil {
		t.Fatal(err)
	}
	bars := indexTestBars(time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local), 60)
	if buyer.Buy("sh600000", bars) {
		t.Fatal("无指数行情时不应买入")
	}
}
