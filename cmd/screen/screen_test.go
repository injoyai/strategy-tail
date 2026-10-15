package main

import (
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/tdx"
	"github.com/injoyai/tdx/lib/xorms"
	"github.com/injoyai/tdx/protocol"
)

func testTradeDB(t *testing.T) *xorms.Engine {
	t.Helper()
	db, err := xorms.NewSqlite(filepath.Join(t.TempDir(), "trades.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Sync2(new(Trade)); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestTradeSellReturnUsesBuyPrice(t *testing.T) {
	for _, tc := range []struct{ buy, sell, want float64 }{{10, 12, 20}, {10, 8, -20}, {10, 0, -100}, {0, 12, 0}} {
		tr := &Trade{BuyPrice: tc.buy}
		tr.Sell(&core.Sell{Price: protocol.Yuan(tc.sell)})
		if tr.Income != tc.want {
			t.Fatalf("%v -> %v: got %v, want %v", tc.buy, tc.sell, tr.Income, tc.want)
		}
	}
}

func TestHistoricalReturnIsCorrectedWithoutUsingLivePrice(t *testing.T) {
	tr := &Trade{BuyPrice: 10, SellPrice: 12, Sold: true, Income: 100.0 / 6}
	tr.Realtime(&protocol.Kline{Close: protocol.Yuan(99)})
	if tr.Income != 20 || tr.SellPrice != 12 {
		t.Fatalf("历史收益率或卖出价不正确: %+v", tr)
	}
}

func TestDiagnoseAllStrategies(t *testing.T) {
	old := common.Manage
	common.Manage = &tdx.Manage{Codes: &tdx.Codes{CodesBase: &tdx.CodesBase{}}}
	t.Cleanup(func() { common.Manage = old })
	s := &ScreenService{DB: testTradeDB(t), Strategies: []Strategy{{Key: "always", Buyer: buy.A全部{}}},
		realtimeDayKlines: map[string]extend.Klines{"sh600000": {
			&extend.Kline{Kline: &protocol.Kline{Time: time.Now(), Close: protocol.Yuan(10)}},
		}},
	}
	for _, key := range []string{"all", ""} {
		got, err := s.Diagnose("sh600000", key)
		if err != nil || !got.Matched {
			t.Fatalf("strategy=%q: got %v, err=%v", key, got, err)
		}
	}
}

func TestHistoryReplacementRollsBackAndClearsEmptyResult(t *testing.T) {
	s := &ScreenService{DB: testTradeDB(t)}
	old := &Trade{Code: "sh600000", BuyTime: "2025-01-02 15:00:00", BuyPrice: 10}
	if _, err := s.DB.Insert(old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`CREATE TRIGGER reject_trade BEFORE INSERT ON trade BEGIN SELECT RAISE(ABORT, 'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	start, _ := time.Parse(time.DateTime, "2025-01-01 00:00:00")
	if err := s.replaceHistoryTrades(start, []*Trade{{Code: "sh600001", BuyTime: old.BuyTime}}); err == nil {
		t.Fatal("插入失败未返回错误")
	}
	rows, err := s.loadingTrades()
	if err != nil || len(rows) != 1 || rows[0].ID != old.ID {
		t.Fatalf("插入失败丢失旧交易: %v, %v", rows, err)
	}
	if err := s.replaceHistoryTrades(start, nil); err != nil {
		t.Fatal(err)
	}
	rows, err = s.loadingTrades()
	if err != nil || len(rows) != 0 {
		t.Fatalf("成功重算为空时应删除旧区间: %v, %v", rows, err)
	}
}

func TestHistoryLoadFailureKeepsOldTrades(t *testing.T) {
	oldManage := common.Manage
	common.Manage = nil
	t.Cleanup(func() { common.Manage = oldManage })
	s := &ScreenService{DB: testTradeDB(t), Codes: []string{"sh600000"}}
	old := &Trade{Code: "sh600000", BuyTime: "2025-01-02 15:00:00"}
	if _, err := s.DB.Insert(old); err != nil {
		t.Fatal(err)
	}
	if err := s.updateHistoryTrade(); err == nil {
		t.Fatal("缺少行情源时应返回错误")
	}
	rows, err := s.loadingTrades()
	if err != nil || len(rows) != 1 || rows[0].ID != old.ID {
		t.Fatalf("行情失败后旧交易发生变化: %v, %v", rows, err)
	}
}

type failingHistoryPool struct {
	tdx.IPool
	err error
}

func (p failingHistoryPool) Do(func(*tdx.Client) error) error { return p.err }

func TestHistoryMinuteFailureKeepsOldTrades(t *testing.T) {
	failure := errors.New("test minute download failure")
	oldManage := common.Manage
	common.Manage = &tdx.Manage{IPool: failingHistoryPool{err: failure}}
	t.Cleanup(func() { common.Manage = oldManage })
	s := &ScreenService{DB: testTradeDB(t), Codes: []string{"sh600000"}, Goroutines: 1,
		Strategies: []Strategy{{Key: "always", Buyer: buy.A全部{}}},
		historyDayKlines: map[string]extend.Klines{"sh600000": {
			&extend.Kline{Kline: &protocol.Kline{Time: time.Now(), Open: 10000, Close: 10000, High: 10000, Low: 10000}},
		}},
	}
	old := &Trade{Code: "sh600000", BuyTime: "2025-01-02 15:00:00"}
	if _, err := s.DB.Insert(old); err != nil {
		t.Fatal(err)
	}
	if err := s.updateHistoryTrade(); !errors.Is(err, failure) {
		t.Fatalf("下载错误未传回调用方: %v", err)
	}
	rows, err := s.loadingTrades()
	if err != nil || len(rows) != 1 || rows[0].ID != old.ID {
		t.Fatalf("下载失败后旧记录被删除: %v, %v", rows, err)
	}
}

func TestResponsesDoNotMutateSharedTrades(t *testing.T) {
	s := &ScreenService{DB: testTradeDB(t), lastPrices: map[string]*protocol.Kline{"sh600000": {Close: protocol.Yuan(12)}}}
	if _, err := s.DB.Insert(&Trade{Code: "sh600000", BuyTime: "2025-01-02", BuyPrice: 10}); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.loadingTrades()
	if err != nil {
		t.Fatal(err)
	}
	loaded[0].Sell(&core.Sell{Price: protocol.Yuan(15)})
	_, _, snapshot := s.snapshot()
	before := *snapshot[0]
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				resp := s.response(TypeHistory, snapshot)
				if resp.Results[0].Sold || resp.Results[0].Income != 20 {
					t.Errorf("持仓快照被调用方修改: %+v", resp.Results[0])
				}
				resp.Results[0].SellPrice = 99
			}
		}()
	}
	wg.Wait()
	if !reflect.DeepEqual(*snapshot[0], before) {
		t.Fatal("构建响应修改了共享交易")
	}
}
