package rule

import (
	"sync"
	"testing"

	f "github.com/injoyai/strategy-tail/strategies/factor"
	"github.com/injoyai/tdx/protocol"
)

func TestMACDEvaluationScope(t *testing.T) {
	ks := t3CloseKlines(10, 12, 11, 9, 8, 13, 14, 12)
	in := NewInput("sh600000", ks, nil, nil)
	for _, params := range [][3]int{{3, 6, 2}, {12, 26, 9}, {3, 6, 2}} {
		op := MACDHist(params[0], params[1], params[2])
		want := (f.MACD柱值{Fast: params[0], Slow: params[1], Signal: params[2]}).Series(ks)
		for end := 1; end <= len(ks); end++ {
			got := op.Value(truncatedInput(in, end))
			if !got.Valid() || got.Float() != want[end-1] {
				t.Fatalf("params=%v prefix=%d: got %v want %g", params, end, got, want[end-1])
			}
		}
	}

	// 同一切片、长度和代码的下一分钟必须重新计算。
	op := MACDHist(3, 6, 2)
	before := op.Value(in).Float()
	ks[len(ks)-1].Close = protocol.Yuan(20)
	want := (f.MACD柱值{Fast: 3, Slow: 6, Signal: 2}).Value("sh600000", ks)
	for _, next := range []Input{NewInput("sh600000", ks, nil, nil), {Code: "sh600000", Klines: ks}} {
		got := op.Value(next)
		if got.Float() == before || got.Float() != want {
			t.Fatalf("下一分钟未重新计算: got %v previous %g want %g", got, before, want)
		}
	}
}

func TestMACDEvaluationScopeConcurrent(t *testing.T) {
	ks := t3CloseKlines(10, 12, 11, 9, 8, 13, 14, 12)
	in := NewInput("sh600000", ks, nil, nil)
	var wg sync.WaitGroup
	for _, params := range [][3]int{{3, 6, 2}, {12, 26, 9}} {
		op := MACDHist(params[0], params[1], params[2])
		want := (f.MACD柱值{Fast: params[0], Slow: params[1], Signal: params[2]}).Series(ks)
		for end := 1; end <= len(ks); end++ {
			wg.Add(1)
			go func(end int) {
				defer wg.Done()
				if got := op.Value(truncatedInput(in, end)); !got.Valid() || got.Float() != want[end-1] {
					t.Errorf("params=%v prefix=%d: got %v want %g", params, end, got, want[end-1])
				}
			}(end)
		}
	}
	wg.Wait()
}

func TestMACDWindowScopeConcurrent(t *testing.T) {
	ks := t3CloseKlines(10, 12, 11, 9, 8, 13, 14, 12)
	in := NewInput("sh600000", ks, nil, nil)
	var wg sync.WaitGroup
	for _, params := range [][3]int{{3, 6, 2}, {12, 26, 9}} {
		for _, includeToday := range []bool{false, true} {
			for _, days := range []int{1, 3, 20} {
				window, err := WindowMax(days, MACDHist(params[0], params[1], params[2]), includeToday)
				if err != nil {
					t.Fatal(err)
				}
				for end := 1; end <= len(ks); end++ {
					hist := (f.MACD柱值{Fast: params[0], Slow: params[1], Signal: params[2]}).Series(ks[:end])
					stop := end
					if !includeToday {
						stop--
					}
					want := InvalidValue()
					if stop > 0 {
						start := max(0, stop-days)
						peak := hist[start]
						for _, v := range hist[start+1 : stop] {
							peak = max(peak, v)
						}
						want = FloatValue(peak)
					}
					wg.Add(1)
					go func() {
						defer wg.Done()
						for repeat := 0; repeat < 16; repeat++ {
							if got := window.Value(truncatedInput(in, end)); got != want {
								t.Errorf("params=%v days=%d prefix=%d includeToday=%v: got=%v want=%v", params, days, end, includeToday, got, want)
								return
							}
						}
					}()
				}
			}
		}
	}
	wg.Wait()
}
