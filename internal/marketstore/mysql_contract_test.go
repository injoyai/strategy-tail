package marketstore

import (
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/injoyai/tdx/v2/extend/storage/storagetest"
)

func TestMySQLBarStoreContract(t *testing.T) {
	storagetest.Run(t, func(t *testing.T) storagetest.Fixture {
		dsn := os.Getenv("PULL_MYSQL_DSN")
		if dsn == "" {
			t.Skip("PULL_MYSQL_DSN not set")
		}
		cfg, err := mysql.ParseDSN(dsn)
		if err != nil {
			t.Fatal("invalid PULL_MYSQL_DSN")
		}
		name := fmt.Sprintf("tdx_contract_%d", time.Now().UnixNano())
		cfg.DBName = ""
		admin, err := sql.Open("mysql", cfg.FormatDSN())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = admin.Close() })
		if _, err := admin.Exec("CREATE DATABASE `" + name + "`"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := admin.Exec("DROP DATABASE `" + name + "`"); err != nil {
				t.Error(err)
			}
		})
		cfg.DBName = name
		store, err := NewMySQLStore(cfg.FormatDSN())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		return storagetest.Fixture{Store: store, Fail: func(t *testing.T, failure storagetest.Failure) {
			var table string
			var err error
			switch failure.Point {
			case storagetest.DayInsert:
				table, err = MySQLDayTable(failure.Code.Market)
			case storagetest.MinuteInsert:
				table, err = MySQLMinuteTable(failure.Code.Market, failure.Year)
			case storagetest.CoverageWrite:
				table = "minute_coverage"
			default:
				t.Fatalf("unsupported failure: %s", failure.Point)
			}
			if err != nil {
				t.Fatal(err)
			}
			// This trigger runs inside the production save transaction.
			if _, err := store.db.Exec("CREATE TRIGGER tdx_contract_failure BEFORE INSERT ON `" + table + "` FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='contract fault'"); err != nil {
				t.Fatal(err)
			}
		}}
	})
}
