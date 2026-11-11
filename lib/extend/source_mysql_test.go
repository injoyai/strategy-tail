package extend

import (
	"os"
	"testing"
	"time"

	"github.com/injoyai/tdx/v2/protocol"
)

// PULL_MYSQL_READ_DSN points to a SELECT-only user on a test database
// seeded with testdata/mysql_readonly.sql.
func TestReadOnlyMarketWithSelectGrant(t *testing.T) {
	dsn := os.Getenv("PULL_MYSQL_READ_DSN")
	if dsn == "" {
		t.Skip("PULL_MYSQL_READ_DSN 未设置")
	}
	reader, err := OpenMarketReader(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	date, err := reader.LatestDayDate("sh000001")
	if err != nil || date != "2026-11-10" {
		t.Fatalf("benchmark date %q: %v", date, err)
	}
	days, err := reader.DayKlinesAll("sh600000")
	if err != nil || len(days) != 1 || days[0].Close != 12 || days[0].FloatStock != 1000 || days[0].Turnover != 10 {
		t.Fatalf("daily bars %v: %v", days, err)
	}
	if name := reader.Metadata().Codes.GetName("sh600000"); name != "浦发银行" {
		t.Fatalf("code snapshot name=%q", name)
	}
	codes, err := reader.LocalStockCodes()
	if err != nil || len(codes) != 1 || codes[0] != "sh600000" {
		t.Fatalf("stock codes %v: %v", codes, err)
	}
	at := time.Date(2026, 11, 10, 9, 32, 0, 0, protocol.Location())
	minutes, err := reader.MinuteKlines("sh600000", at.Add(-time.Minute), at)
	if err != nil || len(minutes) != 2 || !minutes[1].Time.Equal(at) || minutes[1].Close != 11 {
		t.Fatalf("minute bars %v: %v", minutes, err)
	}
	bars, err := reader.StockSessionBars([]string{"sh600000", "sh600001"}, at)
	b := bars["sh600000"]
	if err != nil || len(bars) != 1 || b == nil || b.Open != 10 || b.High != 12 || b.Low != 9 || b.Close != 11 || b.Volume != 3 || b.Amount != 0 || !b.Time.Equal(at) {
		t.Fatalf("session bars %v: %v", bars, err)
	}
	view, err := reader.ResearchView()
	if err != nil {
		t.Fatal(err)
	}
	if _, found := view.Latest("valuation.daily", "sh600000", at); found {
		t.Fatal("future valuation was visible")
	}
	if value, found := view.Latest("valuation.daily", "sh600000", at.Add(8*time.Hour)); !found || value.Values["pe_ttm"] != 12 {
		t.Fatalf("valuation = %+v, %v", value, found)
	}
	if revisions := view.Range("valuation.daily", "sh600000", at, at.Add(8*time.Hour), at.AddDate(0, 0, 1)); len(revisions) != 1 || revisions[0].Values["pe_ttm"] != 13 {
		t.Fatalf("valuation revisions = %+v", revisions)
	}
	u, err := reader.MembershipUniverse()
	if err != nil || u == nil {
		t.Fatalf("membership missing: %v", err)
	}
	if found, err := u.Contains("sh600000", at); !found || err != nil {
		t.Fatalf("member: %v %v", found, err)
	}
}
