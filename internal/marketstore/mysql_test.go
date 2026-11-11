package marketstore

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/extend/storage"
	"github.com/injoyai/tdx/v2/extend/storage/sqlite"
	"github.com/injoyai/tdx/v2/protocol"
)

// newMySQLStore 使用 PULL_MYSQL_DSN 指向的独立测试库；未设置时跳过。
// 测试库不存在时自动创建，测试数据按运行时间生成唯一代码，不清理库内其他数据。
func newMySQLStore(t *testing.T) *MySQLStore {
	t.Helper()
	dsn := os.Getenv("PULL_MYSQL_DSN")
	if dsn == "" {
		t.Skip("未设置 PULL_MYSQL_DSN，跳过 MySQL 契约测试")
	}
	ensureMySQLTestDatabase(t, dsn)
	s, err := NewMySQLStore(dsn)
	if err != nil {
		t.Fatalf("NewMySQLStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// ensureMySQLTestDatabase 先用不带库名的连接建库，避免测试依赖手工初始化。
func ensureMySQLTestDatabase(t *testing.T, dsn string) {
	t.Helper()
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("解析 PULL_MYSQL_DSN: %v", err)
	}
	if cfg.DBName == "" {
		t.Fatal("PULL_MYSQL_DSN 必须带测试库名")
	}
	name := strings.ReplaceAll(cfg.DBName, "`", "``")
	cfg.DBName = ""
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatalf("连接 MySQL: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE DATABASE IF NOT EXISTS `" + name + "` DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
		t.Fatalf("创建测试库: %v", err)
	}
}

var mysqlTestSequence atomic.Uint64

func uniqueMySQLCode() model.Code {
	return model.Code{Market: protocol.MarketAStock, Code: fmt.Sprintf("zz%d_%d", time.Now().UnixNano(), mysqlTestSequence.Add(1))}
}

func TestMySQLStoreIdentity(t *testing.T) {
	s := newMySQLStore(t)
	if s.Identity() == "" || strings.Contains(s.Identity(), "@") {
		t.Fatalf("Identity 不应为空或包含账号信息: %q", s.Identity())
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("重复 Close 应幂等: %v", err)
	}
}

func TestMySQLStoreDay(t *testing.T) {
	s := newMySQLStore(t)
	code := uniqueMySQLCode()
	base := time.Date(2026, 1, 5, 15, 0, 0, 0, time.Local)
	ks := []*model.KlineDay{
		{Time: base.Format(time.DateOnly), Open: 1, High: 2, Low: 0.5, Close: 1.5, Volume: 100, Amount: testAmount(150)},
		{Time: base.AddDate(0, 0, 1).Format(time.DateOnly), Open: 1.5, Close: 2, Volume: 200},
		{Time: base.AddDate(0, 0, 2).Format(time.DateOnly), Open: 2, Close: 2.5, Volume: 300},
	}
	if _, err := s.ImportDayRecords(code, []*DayRecord{
		{KlineDay: *ks[0], Turnover: 1.1, FloatStock: 1000, TotalStock: 2000},
		{KlineDay: *ks[1]}, {KlineDay: *ks[2]},
	}); err != nil {
		t.Fatal(err)
	}
	if last, err := s.LastDayTime(code); err != nil || last != ks[2].Time {
		t.Fatalf("LastDayTime = %q, %v", last, err)
	}
	got, err := s.QueryDay(code, ks[0].Time, ks[1].Time)
	if err != nil {
		t.Fatalf("QueryDay: %v", err)
	}
	if len(got) != 2 || !reflect.DeepEqual(got, ks[:2]) {
		t.Fatalf("QueryDay 升序区间不符: %+v", got)
	}

	// 从第一根起覆盖：删除 Time>=from 后重插，保持与 SQLite 相同的尾替换语义。
	over := []*model.KlineDay{
		{Time: ks[0].Time, Open: 9, Close: 99, Volume: 999},
		{Time: ks[1].Time, Open: 2, Close: 3, Volume: 200},
		{Time: base.AddDate(0, 0, 3).Format(time.DateOnly), Open: 3, Close: 4, Volume: 300},
	}
	if err := s.SaveDay(code, ks[0].Time, over); err != nil {
		t.Fatalf("SaveDay 覆盖: %v", err)
	}
	got, err = s.QueryDay(code, "", "")
	if err != nil {
		t.Fatalf("QueryDay 全量: %v", err)
	}
	if len(got) != 3 || got[0].Open != 9 || got[0].Close != 99 || got[2].Time != over[2].Time {
		t.Fatalf("尾替换结果不符: %+v", got)
	}
	records, err := s.QueryDayRecords(code, "", "")
	if err != nil || len(records) != 3 || records[0].Turnover != 1.1 || records[0].FloatStock != 1000 || records[0].TotalStock != 2000 {
		t.Fatalf("尾替换丢失历史字段: %+v %v", records, err)
	}
	if last, _ := s.LastDayTime(code); last != over[2].Time {
		t.Fatalf("覆盖后 LastDayTime = %q", last)
	}
	// 空数据不写入、不报错。
	if err := s.SaveDay(code, "", nil); err != nil {
		t.Fatal(err)
	}
}

func TestMySQLStoreMinuteAndCoverage(t *testing.T) {
	s := newMySQLStore(t)
	code := uniqueMySQLCode()
	const year = 2099
	base := time.Date(year, 1, 5, 9, 31, 0, 0, time.Local)
	ks := make([]*model.KlineMinute, 0, 3)
	for i := 0; i < 3; i++ {
		ks = append(ks, &model.KlineMinute{
			Time:   base.Add(time.Duration(i) * time.Minute).Format(protocol.TimeLayout),
			Open:   float64(i + 1),
			Close:  float64(i + 1),
			Volume: int64(i+1) * 100,
			Source: model.MinuteSourceTrade,
		})
	}
	if err := s.SaveMinute(code, year, "", ks, nil); err != nil {
		t.Fatalf("SaveMinute: %v", err)
	}
	if last, err := s.LastMinuteTime(code, year); err != nil || last != ks[2].Time {
		t.Fatalf("LastMinuteTime = %q, %v", last, err)
	}
	if last, err := s.LastMinuteTime(code, year-1); err != nil || last != "" {
		t.Fatalf("无分表的年份应返回空: %q, %v", last, err)
	}
	got, err := s.QueryMinute(code, ks[0].Time, ks[1].Time)
	if err != nil {
		t.Fatalf("QueryMinute: %v", err)
	}
	if len(got) != 2 || !reflect.DeepEqual(got, ks[:2]) {
		t.Fatalf("QueryMinute 区间不符: %+v", got)
	}
	// 源时间保持文本口径，证明 parseTime=false 生效。
	if got[0].Time != "2099-01-05 09:31" {
		t.Fatalf("源时间格式不符: %q", got[0].Time)
	}
	if c, err := s.QueryMinuteCoverage(code, year); err != nil || c != nil {
		t.Fatalf("无覆盖记录应返回 nil: %+v, %v", c, err)
	}
	// 原生扫描覆盖。
	if err := s.SaveMinute(code, year, ks[0].Time, ks, &model.MinuteCoverage{From: "2099-01-01 00:00"}); err != nil {
		t.Fatalf("SaveMinute 覆盖: %v", err)
	}
	c, err := s.QueryMinuteCoverage(code, year)
	if err != nil || c == nil || c.From != "2099-01-01 00:00" || c.TradeFallback || c.Through != "" {
		t.Fatalf("覆盖记录不符: %+v, %v", c, err)
	}
	// 切换为分笔兜底覆盖：模式不同不合并。
	if err := s.SaveMinute(code, year, ks[0].Time, ks, &model.MinuteCoverage{From: "2099-02-01 00:00", Through: "2099-03-01 00:00", TradeFallback: true}); err != nil {
		t.Fatalf("SaveMinute 兜底覆盖: %v", err)
	}
	if c, _ := s.QueryMinuteCoverage(code, year); c == nil || c.From != "2099-02-01 00:00" || !c.TradeFallback {
		t.Fatalf("模式切换后覆盖记录不符: %+v", c)
	}
	// 相同模式且区间相接：合并为最宽范围。
	if err := s.SaveMinute(code, year, ks[0].Time, ks, &model.MinuteCoverage{From: "2099-01-15 00:00", Through: "2099-02-15 00:00", TradeFallback: true}); err != nil {
		t.Fatalf("SaveMinute 合并覆盖: %v", err)
	}
	if c, _ := s.QueryMinuteCoverage(code, year); c == nil || c.From != "2099-01-15 00:00" || c.Through != "2099-03-01 00:00" || !c.TradeFallback {
		t.Fatalf("覆盖合并结果不符: %+v", c)
	}
}

func TestMySQLStoreEmptyScanAndMarks(t *testing.T) {
	s := newMySQLStore(t)
	code := uniqueMySQLCode()
	if scan, err := s.LoadMinuteEmptyScan(code, 2096); err != nil || scan != nil {
		t.Fatalf("无空扫描应返回 nil: %+v, %v", scan, err)
	}
	now := time.Unix(1_800_000_000, 0)
	if err := s.SaveMinuteEmptyScan(code, 2096, model.MinuteEmptyScan{From: "2096-01-01 00:00", CheckedAt: now.Unix()}); err != nil {
		t.Fatal(err)
	}
	scan, err := s.LoadMinuteEmptyScan(code, 2096)
	if err != nil || scan == nil || scan.From != "2096-01-01 00:00" || scan.CheckedAt != now.Unix() {
		t.Fatalf("空扫描不符: %+v, %v", scan, err)
	}
	// 覆盖写入。
	if err := s.SaveMinuteEmptyScan(code, 2096, model.MinuteEmptyScan{From: "2096-01-02 00:00", CheckedAt: 2}); err != nil {
		t.Fatal(err)
	}
	if scan, _ := s.LoadMinuteEmptyScan(code, 2096); scan == nil || scan.From != "2096-01-02 00:00" {
		t.Fatalf("空扫描覆盖失败: %+v", scan)
	}

	key := "mysql-test:" + code.Code
	if _, has, err := s.LoadUpdate(key); err != nil || has {
		t.Fatalf("无标记应返回 has=false: %v, %v", has, err)
	}
	if err := s.SaveUpdate(key, 123); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveUpdate(key, 456); err != nil {
		t.Fatal(err)
	}
	if v, has, err := s.LoadUpdate(key); err != nil || !has || v != 456 {
		t.Fatalf("标记读写不符: %d, %v, %v", v, has, err)
	}
}

func TestMySQLStoreBatch(t *testing.T) {
	s := newMySQLStore(t)
	code := uniqueMySQLCode()
	const year = 2099
	base := time.Date(year, 1, 5, 9, 31, 0, 0, time.Local)
	total := mysqlInsertBatch*2 + 7
	ks := make([]*model.KlineMinute, 0, total)
	for i := 0; i < total; i++ {
		ks = append(ks, &model.KlineMinute{Time: base.Add(time.Duration(i) * time.Minute).Format(protocol.TimeLayout)})
	}
	if err := s.SaveMinute(code, year, "", ks, nil); err != nil {
		t.Fatalf("SaveMinute 分批: %v", err)
	}
	got, err := s.QueryMinute(code, "", "")
	if err != nil {
		t.Fatalf("QueryMinute: %v", err)
	}
	if len(got) != total || got[0].Time != ks[0].Time || got[total-1].Time != ks[total-1].Time {
		t.Fatalf("分批写入数量/顺序不符: %d", len(got))
	}
}

func TestMySQLStoreConcurrent(t *testing.T) {
	s := newMySQLStore(t)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	codes := make([]model.Code, 8)
	for i := range codes {
		codes[i] = uniqueMySQLCode()
	}
	for i, code := range codes {
		wg.Add(1)
		go func(i int, code model.Code) {
			defer wg.Done()
			errs <- s.SaveDay(code, "", []*model.KlineDay{{Time: "2026-03-01", Close: float64(i)}})
		}(i, code)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for i, code := range codes {
		got, err := s.QueryDay(code, "", "")
		if err != nil || len(got) != 1 || got[0].Close != float64(i) {
			t.Fatalf("concurrent write=%+v %v", got, err)
		}
	}
}

func TestMySQLMinuteYearsSnapshot(t *testing.T) {
	s := &MySQLStore{tables: map[string]bool{"cn_stock_kline_minute_2024": true, "cn_index_kline_minute_2025": true}, yearsAt: time.Now()}
	snapshot, err := s.minuteYears(context.Background(), protocol.MarketAStock)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot[2025] {
		t.Fatal("another market leaked into the year cache")
	}
	snapshot[2025] = true
	if s.tables["cn_stock_kline_minute_2025"] {
		t.Fatal("year cache leaked a mutable map")
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			years, err := s.minuteYears(context.Background(), protocol.MarketAStock)
			if err != nil {
				t.Error(err)
				return
			}
			for range years {
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 2000; i < 2300; i++ {
			s.mu.Lock()
			s.tables[fmt.Sprintf("cn_stock_kline_minute_%04d", i)] = true
			s.mu.Unlock()
		}
	}()
	wg.Wait()
}

func TestMySQLStoreImportDay(t *testing.T) {
	s := newMySQLStore(t)
	code := uniqueMySQLCode()
	existing := []*model.KlineDay{{Time: "2026-01-03", Close: 99}, {Time: "2026-01-06", Close: 100}}
	if err := s.SaveDay(code, "", existing); err != nil {
		t.Fatal(err)
	}
	history := []*model.KlineDay{{Time: "2026-01-02", Close: 1}, {Time: "2026-01-03", Close: 2}, {Time: "2026-01-05", Close: 3}}
	if n, err := s.ImportDay(code, history); err != nil || n != 2 {
		t.Fatalf("import = %d, %v", n, err)
	}
	if n, err := s.ImportDay(code, history); err != nil || n != 0 {
		t.Fatalf("rerun = %d, %v", n, err)
	}
	got, err := s.QueryDay(code, "", "")
	want := []*model.KlineDay{history[0], existing[0], history[2], existing[1]}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("import changed existing data: %+v, %v", got, err)
	}
}

func TestMySQLStoreImportMinute(t *testing.T) {
	s := newMySQLStore(t)
	code := uniqueMySQLCode()
	const year = 2098
	base := time.Date(year, 1, 5, 9, 31, 0, 0, time.UTC)
	history := make([]*model.KlineMinute, mysqlInsertBatch*2+7)
	for i := range history {
		history[i] = &model.KlineMinute{Time: base.Add(time.Duration(i) * time.Minute).Format(protocol.TimeLayout), Close: float64(i), Source: model.MinuteSourceTrade}
	}
	first := *history[0]
	first.Close = 99
	first.Source = ""
	last := *history[len(history)-1]
	last.Close = 100
	last.Source = ""
	tail := &model.KlineMinute{Time: "2098-12-31 15:00", Close: 101}
	oldCoverage := &model.MinuteCoverage{From: "2098-12-01 00:00", Through: tail.Time, TradeFallback: true}
	if err := s.SaveMinute(code, year, "", []*model.KlineMinute{&first, &last, tail}, oldCoverage); err != nil {
		t.Fatal(err)
	}
	// Existing first+last do not prove that the middle has been imported.
	coverage := &model.MinuteCoverage{From: "2098-01-01 00:00"}
	if n, err := s.ImportMinute(code, year, history, coverage); err != nil || n != len(history)-2 {
		t.Fatalf("import = %d, %v", n, err)
	}
	if n, err := s.ImportMinute(code, year, history, coverage); err != nil || n != 0 {
		t.Fatalf("rerun = %d, %v", n, err)
	}
	got, err := s.QueryMinute(code, "", "")
	want := append([]*model.KlineMinute(nil), history...)
	want[0] = &first
	want[len(history)-1] = &last
	want = append(want, tail)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("import lost data/source: rows=%d want=%d err=%v", len(got), len(want), err)
	}
	mark, err := s.QueryMinuteCoverage(code, year)
	if err != nil || mark == nil || mark.From != oldCoverage.From || !mark.TradeFallback {
		t.Fatalf("existing coverage changed: %+v %v", mark, err)
	}
	// A failure in a later batch must roll back the earlier full batch.
	other := uniqueMySQLCode()
	invalid := append([]*model.KlineMinute(nil), history[:mysqlInsertBatch+1]...)
	bad := *invalid[len(invalid)-1]
	bad.Source = strings.Repeat("x", 9)
	invalid[len(invalid)-1] = &bad
	if _, err := s.ImportMinute(other, year, invalid, nil); err == nil {
		t.Fatal("overlong source accepted")
	}
	if got, err := s.QueryMinute(other, "", ""); err != nil || len(got) != 0 {
		t.Fatalf("failed import left rows=%d, %v", len(got), err)
	}
}

func TestMySQLStoreCaseSensitiveCodes(t *testing.T) {
	s := newMySQLStore(t)
	a := model.Code{Market: protocol.MarketFuture, Code: "cff/" + uniqueMySQLCode().Code + "Ab"}
	b := a
	b.Code = strings.TrimSuffix(a.Code, "Ab") + "ab"
	if err := s.SaveDay(a, "", []*model.KlineDay{{Time: "2026-01-02", Close: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDay(b, "", []*model.KlineDay{{Time: "2026-01-02", Close: 2}}); err != nil {
		t.Fatal(err)
	}
	codes, err := s.DayCodes(protocol.MarketFuture)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, c := range codes {
		found[c.Code] = true
	}
	if !found[a.Code] || !found[b.Code] {
		t.Fatalf("missing historical codes: %v", codes)
	}
	for i, c := range []model.Code{a, b} {
		got, err := s.QueryDay(c, "", "")
		if err != nil || len(got) != 1 || got[0].Close != float64(i+1) {
			t.Fatalf("case collision: rows=%+v err=%v", got, err)
		}
	}
}

func TestMySQLStoreMatchesSQLite(t *testing.T) {
	mysql := newMySQLStore(t)
	sqlite, err := sqlite.NewPull(t.TempDir(), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlite.Close()
	code := uniqueMySQLCode()
	stores := []storage.BarStore{sqlite, mysql}
	for _, rows := range [][]*model.KlineDay{
		{{Time: "2026-01-02", Open: 1.00001, Close: 1.00002, Amount: testAmount(0.00004), Volume: 100}, {Time: "2026-01-05", Close: 2}},
		{{Time: "2026-01-06", Close: 3}},
		nil,
	} {
		for _, s := range stores {
			if err := s.SaveDay(code, "", rows); err != nil {
				t.Fatal(err)
			}
		}
		want, err := sqlite.QueryDay(code, "", "")
		if err != nil {
			t.Fatal(err)
		}
		got, err := mysql.QueryDay(code, "", "")
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("day differs from SQLite: got=%+v want=%+v err=%v", got, want, err)
		}
	}
	for _, rows := range [][]*model.KlineMinute{
		{{Time: "2095-01-02 09:31", Close: -0.0001, Volume: 100, Source: model.MinuteSourceTrade}, {Time: "2095-01-02 09:32", Close: 2}},
		{{Time: "2095-01-03 09:31", Close: 3, Source: model.MinuteSourceTrade}},
		nil,
	} {
		for _, s := range stores {
			if err := s.SaveMinute(code, 2095, "", rows, nil); err != nil {
				t.Fatal(err)
			}
		}
		want, err := sqlite.QueryMinute(code, "", "")
		if err != nil {
			t.Fatal(err)
		}
		got, err := mysql.QueryMinute(code, "", "")
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("minute differs from SQLite: got=%+v want=%+v err=%v", got, want, err)
		}
	}
}

func TestMySQLMarketTableNames(t *testing.T) {
	for _, market := range []protocol.Market{protocol.MarketAStock, protocol.MarketIndex, protocol.MarketHK, protocol.MarketUS, protocol.MarketFuture} {
		got, err := MySQLMinuteTable(market, 2025)
		if err != nil || got != strings.ReplaceAll(string(market), "/", "_")+"_kline_minute_2025" {
			t.Fatalf("table=%q err=%v", got, err)
		}
	}
	for _, market := range []protocol.Market{"", "cn_stock", "cn/st_ock", "CN/stock", "cn/stock`;DROP TABLE x", "cn/stock/extra"} {
		if _, err := MySQLDayTable(market); err == nil {
			t.Fatalf("accepted unsafe or ambiguous market: %q", market)
		}
	}
	if _, err := MySQLMinuteTable(protocol.MarketAStock, 10000); err == nil {
		t.Fatal("accepted invalid year")
	}
}

func TestMySQLMarketIsolation(t *testing.T) {
	s := newMySQLStore(t)
	name := uniqueMySQLCode().Code
	stock := model.Code{Market: protocol.MarketAStock, Code: name}
	index := model.Code{Market: protocol.MarketIndex, Code: name}
	a := []*model.KlineMinute{{Time: "2097-01-01 09:31", Close: 1, Source: model.MinuteSourceTrade}}
	b := []*model.KlineMinute{{Time: "2098-01-01 09:31", Close: 2}}
	if err := s.SaveMinute(stock, 2097, "", a, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMinute(index, 2098, "", b, nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		code model.Code
		want []*model.KlineMinute
	}{{stock, a}, {index, b}} {
		got, err := s.QueryMinute(tc.code, "2097-01-01 00:00", "2098-12-31 23:59")
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("market leak: %v %v", got, err)
		}
	}
	if got, err := s.LastMinuteTime(index, 2097); err != nil || got != "" {
		t.Fatalf("wrong market year=%q %v", got, err)
	}
	if err := s.SaveDay(stock, "", []*model.KlineDay{{Time: "2025-01-01", Close: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDay(index, "", []*model.KlineDay{{Time: "2025-01-01", Close: 2}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.QueryDay(index, "", "")
	if err != nil || len(got) != 1 || got[0].Close != 2 {
		t.Fatalf("day market leak: %v %v", got, err)
	}
}

func TestMySQLImportStorePendingLayout(t *testing.T) {
	dsn := os.Getenv("PULL_MYSQL_DSN")
	if dsn == "" {
		t.Skip("PULL_MYSQL_DSN not set")
	}
	cfg, e := mysql.ParseDSN(dsn)
	if e != nil {
		t.Fatal("bad test DSN")
	}
	name := fmt.Sprintf("split_import_gate_%d", time.Now().UnixNano())
	cfg.DBName = name
	isolated := cfg.FormatDSN()
	ensureMySQLTestDatabase(t, isolated)
	adminCfg := *cfg
	adminCfg.DBName = ""
	admin, e := sql.Open("mysql", adminCfg.FormatDSN())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { admin.Exec("DROP DATABASE `" + name + "`"); admin.Close() })
	s, e := NewMySQLStore(isolated)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec("CREATE TABLE kline_schema(version VARCHAR(32) PRIMARY KEY,ready TINYINT NOT NULL)"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec("INSERT INTO kline_schema VALUES('market-v1',0)"); e != nil {
		t.Fatal(e)
	}
	s.Close()
	if normal, e := NewMySQLStore(isolated); e == nil {
		normal.Close()
		t.Fatal("normal application opened pending layout")
	}
	importer, e := NewMySQLImportStore(isolated)
	if e != nil {
		t.Fatal(e)
	}
	defer importer.Close()
	code := model.Code{Market: protocol.MarketAStock, Code: "sh600000"}
	if n, e := importer.ImportMinute(code, 2000, []*model.KlineMinute{{Time: "2000-01-04 09:31", Close: 1.23456789, Volume: 9876543210}}, nil); e != nil || n != 1 {
		t.Fatal("pending import failed", n, e)
	}
	if _, e = importer.db.Exec("CREATE TABLE kline_minute_2001(id INT)"); e != nil {
		t.Fatal(e)
	}
	if bad, e := NewMySQLImportStore(isolated); e == nil {
		bad.Close()
		t.Fatal("opened mixed legacy layout before migration")
	}
}
