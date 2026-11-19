package lab

import (
	"context"
	"errors"
	common "github.com/injoyai/strategy-tail"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/internal/marketstore"
	"github.com/injoyai/strategy-tail/internal/testmarket"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/protocol"
)

type countingResearchStore struct {
	*testmarket.Store
	calls [][2]string
	fail  bool
}

func (s *countingResearchStore) ReadStockTurnover(_ context.Context, from, to string, visit func(marketstore.StockTurnover)) error {
	s.calls = append(s.calls, [2]string{from, to})
	if s.fail {
		return errors.New("read failed")
	}
	for _, day := range []string{"2024-01-20", "2024-02-20", "2024-03-20"} {
		if from <= day && day <= to {
			visit(marketstore.StockTurnover{Date: day, Code: "sh600000", Amount: 100})
		}
	}
	return nil
}
func TestResearchCachePersistsAndOnlyComputesMissingMonths(t *testing.T) {
	store := &countingResearchStore{Store: testmarket.New()}
	var bars []*model.KlineDay
	for i := 0; i < 30; i++ {
		day := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i)
		bars = append(bars, &model.KlineDay{Time: day.Format(time.DateOnly), Close: float64(100 + i)})
	}
	store.SeedDay(model.Code{Market: protocol.MarketIndex, Code: "sh000001"}, bars)
	market := extend.NewMarketReader(store, nil)
	path := filepath.Join(t.TempDir(), "cache.sqlite")
	cache := &indexResearchCache{path: path}
	loc := protocol.Location()
	date := func(s string) time.Time {
		v, e := time.ParseInLocation(time.DateOnly, s, loc)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	now := date("2024-04-10").Add(16 * time.Hour)
	query := func(c *indexResearchCache, start, end string) []indexResearchPoint {
		t.Helper()
		p, e := c.query(context.Background(), market, "sh000001", date(start), date(end), 5, now)
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	first := query(cache, "2024-01-01", "2024-01-31")
	if len(store.calls) != 1 || store.calls[0] != [2]string{"2024-01-01", "2024-01-31"} {
		t.Fatalf("initial reads: %v", store.calls)
	}
	if first[14].RSI == nil || *first[14].RSI != 100 {
		t.Fatalf("incorrect RSI: %+v", first[14])
	}
	query(&indexResearchCache{path: path}, "2024-01-10", "2024-01-31")
	if len(store.calls) != 1 {
		t.Fatalf("reopened cache scanned again: %v", store.calls)
	}
	query(cache, "2024-01-01", "2024-03-31")
	if len(store.calls) != 2 || store.calls[1] != [2]string{"2024-02-01", "2024-03-31"} {
		t.Fatalf("expected only missing months: %v", store.calls)
	}
}
func TestResearchCacheRetriesFailedReads(t *testing.T) {
	store := &countingResearchStore{Store: testmarket.New(), fail: true}
	market := extend.NewMarketReader(store, nil)
	cache := &indexResearchCache{path: filepath.Join(t.TempDir(), "cache.sqlite")}
	day := time.Date(2024, 1, 1, 0, 0, 0, 0, protocol.Location())
	now := time.Date(2024, 4, 1, 16, 0, 0, 0, protocol.Location())
	if _, err := cache.query(context.Background(), market, "sh000001", day, day, 5, now); err == nil {
		t.Fatal("read failure lost")
	}
	store.fail = false
	if _, err := cache.query(context.Background(), market, "sh000001", day, day, 5, now); err != nil || len(store.calls) != 2 {
		t.Fatalf("failed month must be retried: %v, %v", err, store.calls)
	}
}

func TestResearchCacheRefreshesOnlyRecentTail(t *testing.T) {
	store := &countingResearchStore{Store: testmarket.New()}
	market := extend.NewMarketReader(store, nil)
	cache := &indexResearchCache{path: filepath.Join(t.TempDir(), "cache.sqlite")}
	loc := protocol.Location()
	start := time.Date(2024, 3, 1, 0, 0, 0, 0, loc)
	now := time.Date(2024, 3, 25, 16, 10, 0, 0, loc)
	for _, at := range []time.Time{now, now.Add(20 * time.Minute), now.Add(time.Hour)} {
		if _, err := cache.query(context.Background(), market, "sh000001", start, now, 5, at); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.calls) != 2 || store.calls[0] != [2]string{"2024-03-01", "2024-03-25"} || store.calls[1] != [2]string{"2024-03-19", "2024-03-25"} {
		t.Fatalf("unexpected cache refresh spans: %v", store.calls)
	}
}

func TestResearchCacheMySQLReadOnlyIntegration(t *testing.T) {
	if os.Getenv("INDEX_RESEARCH_LIVE_TEST") != "1" {
		t.Skip("set INDEX_RESEARCH_LIVE_TEST=1 for read-only MySQL integration")
	}
	market, err := common.OpenResearchMarket()
	if err != nil {
		t.Fatal("cannot open market data")
	}
	defer market.Close()
	loc := protocol.Location()
	from := time.Date(2026, 11, 10, 0, 0, 0, 0, loc)
	to := time.Date(2026, 11, 17, 0, 0, 0, 0, loc)
	now := time.Date(2026, 11, 17, 19, 0, 0, 0, loc)
	cache := &indexResearchCache{path: filepath.Join(t.TempDir(), "cache.sqlite")}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	first, err := cache.query(ctx, market, "sh000001", from, to, 5, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) == 0 {
		t.Fatal("no index data returned")
	}
	second, err := cache.query(ctx, market, "sh000001", from, to, 5, now)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("cached result differs: %v", err)
	}
	var rsi, share int
	for _, p := range first {
		if p.RSI != nil {
			rsi++
		}
		if p.Concentration != nil && p.Concentration.Share != nil {
			share++
		}
	}
	if rsi == 0 || share == 0 {
		t.Fatalf("missing RSI or concentration: %d %d", rsi, share)
	}
}
