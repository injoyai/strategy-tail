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
	"github.com/injoyai/strategy-tail/internal/testmarket"
	"github.com/injoyai/strategy-tail/internal/xorms"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/tdx/v2/extend/metadata"
	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/extend/storage"
	"github.com/injoyai/tdx/v2/protocol"
)

func testTradeDB(t *testing.T) *xorms.Engine {
	t.Helper()
	db, err := xorms.NewSQLite(filepath.Join(t.TempDir(), "trades.db"))
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
		tr.Sell(&core.Sell{Price: tc.sell})
		if tr.Income != tc.want {
			t.Fatalf("%v -> %v: got %v, want %v", tc.buy, tc.sell, tr.Income, tc.want)
		}
	}
}

func TestHistoricalReturnIsCorrectedWithoutUsingLivePrice(t *testing.T) {
	tr := &Trade{BuyPrice: 10, SellPrice: 12, Sold: true, Income: 100.0 / 6}
	tr.Realtime(&protocol.Kline{Close: 99})
	if tr.Income != 20 || tr.SellPrice != 12 {
		t.Fatalf("历史收益率或卖出价不正确: %+v", tr)
	}
}

func TestDiagnoseAllStrategies(t *testing.T) {
	old := common.Manage
	catalog, err := metadata.NewCodes(nil)
	if err != nil {
		t.Fatal(err)
	}
	common.Manage = &extend.MarketData{Codes: catalog}
	t.Cleanup(func() { common.Manage = old })
	s := &ScreenService{DB: testTradeDB(t), Strategies: []Strategy{{Key: "always", Buyer: buy.A全部{}}},
		realtimeDayKlines: map[string]extend.Klines{"sh600000": {
			&extend.Kline{Kline: &protocol.Kline{Time: time.Now(), Close: 10}},
		}},
	}
	for _, key := range []string{"all", ""} {
		got, err := s.Diagnose("sh600000", key)
		if err != nil || !got.Matched {
			t.Fatalf("strategy=%q: got %v, err=%v", key, got, err)
		}
	}
}

func TestDiagnoseChartIncludesHistoryBeforeStrategyCache(t *testing.T) {
	oldPull, oldManage := common.Pull, common.Manage
	catalog, err := metadata.NewCodes(nil)
	if err != nil {
		t.Fatal(err)
	}
	common.Manage = &extend.MarketData{Codes: catalog}
	dir := t.TempDir()
	store := testmarket.Open(t, dir)
	common.Pull = &extend.PullKline{Config: extend.PullKlineConfig{Dir: dir, Store: store}}
	t.Cleanup(func() { common.Pull, common.Manage = oldPull, oldManage })
	base := time.Date(2020, 1, 1, 15, 0, 0, 0, protocol.Location())
	ks := extend.Klines{}
	var rows []*model.KlineDay
	for i := 0; i < 400; i++ {
		day := base.AddDate(0, 0, i)
		rows = append(rows, &model.KlineDay{Time: day.Format(time.DateOnly), Open: 10, Close: 10, High: 11, Low: 9})
		ks = append(ks, &extend.Kline{Unix: day.Unix(), Kline: &protocol.Kline{
			Time: day, Open: 10, Close: 10, High: 11, Low: 9}})
	}
	if err := store.SaveDay(model.Code{Market: protocol.MarketAStock, Code: "sh600000"}, "", rows); err != nil {
		t.Fatal(err)
	}
	// 缓存中的末日价格包含尚未落库的实时变化。
	live := *ks[399].Kline
	live.Close = 12
	s := &ScreenService{DB: testTradeDB(t), Strategies: []Strategy{{Key: "always", Name: "全部", Buyer: buy.A全部{}}},
		realtimeDayKlines: map[string]extend.Klines{"sh600000": {{Unix: ks[399].Unix, Kline: &live}}}}
	got, err := s.Diagnose("sh600000", "always")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Klines) != 400 || got.Klines[0].Time != "2020-01-01" || got.Klines[399].Close != 12 {
		t.Fatal("图表应包含完整本地历史，并保留缓存中的最新行情")
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

type failingHistorySource struct {
	storage.BarStore
	err error
}

func (p failingHistorySource) QueryMinute(model.Code, string, string) ([]*model.KlineMinute, error) {
	return nil, p.err
}

func TestHistoryMinuteFailureKeepsOldTrades(t *testing.T) {
	failure := errors.New("test minute read failure")
	oldManage, oldPull := common.Manage, common.Pull
	common.Manage = &extend.MarketData{}
	common.Pull = &extend.PullKline{Config: extend.PullKlineConfig{Store: failingHistorySource{err: failure}}}
	t.Cleanup(func() { common.Manage, common.Pull = oldManage, oldPull })
	s := &ScreenService{DB: testTradeDB(t), Codes: []string{"sh600000"}, Goroutines: 1,
		Strategies: []Strategy{{Key: "always", Buyer: buy.A全部{}}},
		historyDayKlines: map[string]extend.Klines{"sh600000": {
			&extend.Kline{Kline: &protocol.Kline{Time: time.Now(), Open: 10.0, Close: 10.0, High: 10.0, Low: 10.0}},
		}},
	}
	old := &Trade{Code: "sh600000", BuyTime: "2025-01-02 15:00:00"}
	if _, err := s.DB.Insert(old); err != nil {
		t.Fatal(err)
	}
	if err := s.updateHistoryTrade(); !errors.Is(err, failure) {
		t.Fatalf("分钟读取错误未传回调用方: %v", err)
	}
	rows, err := s.loadingTrades()
	if err != nil || len(rows) != 1 || rows[0].ID != old.ID {
		t.Fatalf("分钟读取失败后旧记录被删除: %v, %v", rows, err)
	}
}

func TestResponsesDoNotMutateSharedTrades(t *testing.T) {
	s := &ScreenService{DB: testTradeDB(t), lastPrices: map[string]*protocol.Kline{"sh600000": {Close: 12}}}
	if _, err := s.DB.Insert(&Trade{Code: "sh600000", BuyTime: "2025-01-02", BuyPrice: 10}); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.loadingTrades()
	if err != nil {
		t.Fatal(err)
	}
	loaded[0].Sell(&core.Sell{Price: 15})
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
