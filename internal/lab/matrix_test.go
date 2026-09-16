package lab

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/lib/xorms"
	"github.com/injoyai/tdx/protocol"
)

// writeDayDBCloses 按收盘价序列写伪造日K sqlite（表结构同 server_test.go
// writeFakeDayDB；区别：每日收盘可指定，构造有方向的因子序列）。
func writeDayDBCloses(t *testing.T, dir, code string, closes []float64, base time.Time) {
	t.Helper()
	db, err := xorms.NewSqlite(filepath.Join(dir, extend.DirDay, code+".db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Sync2(new(extend.Kline)); err != nil {
		t.Fatal(err)
	}
	rows := make([]*extend.Kline, 0, len(closes))
	for i, c := range closes {
		tm := base.AddDate(0, 0, i)
		rows = append(rows, &extend.Kline{
			Unix: tm.Unix(),
			Kline: &protocol.Kline{
				Time: tm, Open: protocol.Yuan(c), Close: protocol.Yuan(c),
				High: protocol.Yuan(c), Low: protocol.Yuan(c), Volume: 10000,
			},
		})
	}
	if _, err := db.Insert(rows); err != nil {
		t.Fatal(err)
	}
}

// fnFac 闭包因子：动量 = 末收盘/前第 2 根收盘 − 1，不足 3 根返回 NaN。
// 注意 protocol.Price 是 int64（厘），须转 float64 再做除法。
type fnFac struct{ name string }

func (f fnFac) Name() string { return f.name }
func (f fnFac) Value(_ string, dks extend.Klines) float64 {
	if len(dks) < 3 {
		return math.NaN()
	}
	return dks[len(dks)-1].Close.Float64()/dks[len(dks)-3].Close.Float64() - 1
}

// TestFillCrossSection 走伪造日K DB + ForEachCodeData 真实路径：
// 升票第一、并列票按代码字典序破平、降票垫底；同时覆盖 asc 方向、
// 多 key 隔离与 NaN 不参与排名。
func TestFillCrossSection(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	old := common.Pull
	common.Pull = &extend.PullKline{Config: extend.PullKlineConfig{Dir: dir}}
	t.Cleanup(func() { common.Pull = old })

	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	writeDayDBCloses(t, dir, "sh600001", []float64{10, 11, 12}, base)
	writeDayDBCloses(t, dir, "sh600002", []float64{20, 18, 16}, base)
	writeDayDBCloses(t, dir, "sh600003", []float64{10, 11, 12}, base) // 与 001 并列

	fa := fnFac{name: "动量"}
	reqs := []topNReq{
		{factor: fa, key: core.TopNKey(fa.Name(), false), asc: false},
		{factor: fa, key: core.TopNKey(fa.Name(), true), asc: true},
	}

	if err := fillCrossSection(context.Background(), []string{"sh600001", "sh600002", "sh600003"}, []int{2025}, reqs); err != nil {
		t.Fatal(err)
	}
	defer core.ClearCrossSection()

	// 前两天因子不足 3 根返回 NaN，两个 key 均不产生快照
	for i, d := range []time.Time{base, base.AddDate(0, 0, 1)} {
		day := core.DayOf(d)
		if got := core.CrossSectionRank(day, reqs[0].key, "sh600001"); got != 0 {
			t.Fatalf("第 %d 天 NaN，desc 快照不应存在: 名次 = %d", i+1, got)
		}
		if got := core.CrossSectionRank(day, reqs[1].key, "sh600002"); got != 0 {
			t.Fatalf("第 %d 天 NaN，asc 快照不应存在: 名次 = %d", i+1, got)
		}
	}

	day := core.DayOf(base.AddDate(0, 0, 2))
	// desc：动量大者第一，并列按代码字典序破平
	if got := core.CrossSectionRank(day, reqs[0].key, "sh600001"); got != 1 {
		t.Fatalf("sh600001 名次 = %d (期望 1)", got)
	}
	if got := core.CrossSectionRank(day, reqs[0].key, "sh600003"); got != 2 {
		t.Fatalf("sh600003 并列破平名次 = %d (期望 2)", got)
	}
	if got := core.CrossSectionRank(day, reqs[0].key, "sh600002"); got != 3 {
		t.Fatalf("sh600002 名次 = %d (期望 3)", got)
	}
	// asc：动量小者第一，并列同样按代码字典序破平
	if got := core.CrossSectionRank(day, reqs[1].key, "sh600002"); got != 1 {
		t.Fatalf("sh600002 asc 名次 = %d (期望 1)", got)
	}
	if got := core.CrossSectionRank(day, reqs[1].key, "sh600001"); got != 2 {
		t.Fatalf("sh600001 asc 并列破平名次 = %d (期望 2)", got)
	}
	if got := core.CrossSectionRank(day, reqs[1].key, "sh600003"); got != 3 {
		t.Fatalf("sh600003 asc 名次 = %d (期望 3)", got)
	}
}
