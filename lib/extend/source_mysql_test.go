package extend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/internal/marketstore"
	"github.com/injoyai/tdx/v2"
	"github.com/injoyai/tdx/v2/extend/pull"
	"github.com/injoyai/tdx/v2/protocol"
)

type mysqlUpdateClient struct {
	tdx.Client
	calls int
}

func (c *mysqlUpdateClient) GetKlineUntil(category uint8, code string, stop func(tdx.Kline) bool) (*tdx.KlineResp, error) {
	c.calls++
	at := time.Now().In(protocol.Location()).Format(time.DateOnly)
	if category == protocol.TypeKlineMinute {
		at += " 09:31"
	}
	amount := 1100.0
	return &tdx.KlineResp{List: []tdx.Kline{{Time: at, Open: 10, High: 12, Low: 9, Close: 11, Volume: 100, Amount: &amount}}}, nil
}

type mysqlUpdateStore struct {
	*marketstore.MySQLStore
	run string
}

func (s mysqlUpdateStore) Identity() string { return s.MySQLStore.Identity() + "#" + s.run }

type mysqlUpdatePool struct {
	tdx.ClientPool
	client tdx.Client
}

func (p mysqlUpdatePool) DoContext(_ context.Context, f func(tdx.Client) error) error {
	return f(p.client)
}

func TestMySQLUpdateStoresAllMarketDataAndMarksRemotely(t *testing.T) {
	dsn := os.Getenv("PULL_MYSQL_DSN")
	if dsn == "" {
		t.Skip("PULL_MYSQL_DSN 未设置")
	}
	store, err := marketstore.NewMySQLStore(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	root := t.TempDir()
	start := time.Now().In(protocol.Location())
	start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, protocol.Location())
	target := mysqlUpdateStore{MySQLStore: store, run: time.Now().Format("150405.000000000")}
	p, err := NewPullKline(PullKlineConfig{Store: target, Dir: root, Codes: []string{"sh000001"}, Types: []string{Day, Minute}, StartAt: start})
	if err != nil {
		t.Fatal(err)
	}
	client := &mysqlUpdateClient{}
	if err := p.UpdateWithProgress(&MarketData{ClientPool: mysqlUpdatePool{client: client}}, true, nil); err != nil {
		t.Fatal(err)
	}
	if client.calls != 2 {
		t.Fatalf("SDK calls=%d, want day+minute", client.calls)
	}
	if !strings.HasPrefix(p.StorageIdentity(), "mysql://") {
		t.Fatal("update did not use MySQL")
	}
	code, err := pull.ParseCode("sh000001")
	if err != nil {
		t.Fatal(err)
	}
	days, err := store.QueryDay(code, start.Format(time.DateOnly), start.Format(time.DateOnly))
	if err != nil || len(days) != 1 || days[0].Close != 11 {
		t.Fatal("MySQL day update missing", days, err)
	}
	minutes, err := store.QueryMinute(code, start.Format(protocol.TimeLayout), start.AddDate(0, 0, 1).Format(protocol.TimeLayout))
	if err != nil || len(minutes) != 1 || minutes[0].Close != 11 {
		t.Fatal("MySQL minute update missing", minutes, err)
	}
	if unix, has, err := store.LoadUpdate("pull"); err != nil || !has || unix <= 0 {
		t.Fatalf("MySQL mark missing: %d %v %v", unix, has, err)
	}
	if last, err := p.LastSuccessfulUpdate(); err != nil || last.IsZero() {
		t.Fatal("MySQL update status missing", last, err)
	}
	for _, name := range []string{"update.db", "source", DirDay, DirMinute} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("update created local market storage: %s %v", name, err)
		}
	}
}

func TestReadOnlyMarketWithSelectGrant(t *testing.T) {
	dsn := os.Getenv("PULL_MYSQL_READ_DSN")
	if dsn == "" {
		t.Skip("PULL_MYSQL_READ_DSN 未设置")
	}
	reader, err := OpenReadOnlyPullKline(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	date, err := reader.LatestDayDate("sh000001")
	if err != nil || date != "2026-11-10" {
		t.Fatalf("benchmark date %q: %v", date, err)
	}
	days, err := reader.DayKlinesAll("sh600000")
	if err != nil || len(days) != 1 || days[0].Close != 12 {
		t.Fatalf("daily bars %v: %v", days, err)
	}
	if err := reader.Update(nil); !errors.Is(err, ErrReadOnlyMarket) {
		t.Fatalf("write path returned %v", err)
	}
}
