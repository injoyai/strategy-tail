package marketstore

import (
	"database/sql"
	"strings"
	"testing"

	_ "github.com/glebarez/go-sqlite"
	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/protocol"
)

func TestMySQLReaderQueriesDayTablesWithoutDDL(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
CREATE TABLE cn_stock_kline_day (code TEXT);
INSERT INTO cn_stock_kline_day VALUES ('sh600000'), ('sz000001'), ('sh600000');
PRAGMA query_only=ON;
`); err != nil {
		t.Fatal(err)
	}
	reader := &MySQLReader{store: &MySQLStore{db: db}}
	codes, err := reader.DayCodes(protocol.MarketAStock)
	if err != nil || len(codes) != 2 || codes[0].Code != "sh600000" || codes[1].Code != "sz000001" {
		t.Fatalf("DayCodes = %v, %v", codes, err)
	}
	_, err = reader.QueryDayRecords(model.Code{Market: protocol.MarketIndex, Code: "sh000001"}, "", "")
	if err == nil || !strings.Contains(err.Error(), "no such table: cn_index_kline_day") {
		t.Fatalf("QueryDayRecords should report the missing table: %v", err)
	}
}
