package core

import (
	"math/rand"
	"testing"
	"time"
)

func TestCrossSection_写入查询与名次(t *testing.T) {
	ClearCrossSection()
	defer ClearCrossSection()

	day := DayOf(time.Date(2025, 1, 6, 0, 0, 0, 0, time.Local))
	key := TopNKey("N日动量(20)", false) // 降序
	SetCrossSection(day, key, []string{"sz000003", "sh600001", "sh600002"})

	if r := CrossSectionRank(day, key, "sz000003"); r != 1 {
		t.Fatalf("榜首名次应=1, got %d", r)
	}
	if r := CrossSectionRank(day, key, "sh600002"); r != 3 {
		t.Fatalf("榜尾名次应=3, got %d", r)
	}
	if r := CrossSectionRank(day, key, "sh600009"); r != 0 {
		t.Fatalf("不在快照中名次应=0, got %d", r)
	}
	// 不同日期隔离
	other := day.AddDate(0, 0, 1)
	if r := CrossSectionRank(other, key, "sz000003"); r != 0 {
		t.Fatalf("跨日期不应命中, got %d", r)
	}
}

func TestCrossSection_方向key隔离互不覆盖(t *testing.T) {
	ClearCrossSection()
	defer ClearCrossSection()

	day := DayOf(time.Date(2025, 1, 6, 0, 0, 0, 0, time.Local))
	asc := TopNKey("量比(5)", true)
	desc := TopNKey("量比(5)", false)
	SetCrossSection(day, asc, []string{"sh600002", "sh600001"})
	SetCrossSection(day, desc, []string{"sh600001", "sh600002"})

	if CrossSectionRank(day, asc, "sh600002") != 1 || CrossSectionRank(day, desc, "sh600002") != 2 {
		t.Fatal("同因子不同方向的快照被覆盖")
	}
}

func TestCrossSection_并发读写安全(t *testing.T) {
	ClearCrossSection()
	defer ClearCrossSection()

	day := DayOf(time.Date(2025, 1, 6, 0, 0, 0, 0, time.Local))
	done := make(chan struct{})
	go func() { // 读协程
		for {
			select {
			case <-done:
				return
			default:
				CrossSectionRank(day, "k", "c1")
			}
		}
	}()
	for i := 0; i < 100; i++ {
		SetCrossSection(day, "k", []string{"c1", "c2"})
	}
	close(done)
}

func TestCrossSection_重复Set同日同key覆盖(t *testing.T) {
	ClearCrossSection()
	defer ClearCrossSection()

	day := DayOf(time.Date(2025, 1, 6, 0, 0, 0, 0, time.Local))
	SetCrossSection(day, "k", []string{"a", "b"})
	SetCrossSection(day, "k", []string{"b", "a"})
	if CrossSectionRank(day, "k", "b") != 1 || CrossSectionRank(day, "k", "a") != 2 {
		t.Fatal("重复 Set 应整体覆盖")
	}
}

// Set/CrossSectionRank 内部都做 DayOf 归一：带时分秒写入、零点查询应命中。
func TestCrossSection_时间参数DayOf归一(t *testing.T) {
	ClearCrossSection()
	defer ClearCrossSection()

	intraday := time.Date(2025, 1, 6, 14, 55, 0, 0, time.Local)
	midnight := DayOf(intraday)
	SetCrossSection(intraday, "k", []string{"c1"})
	if r := CrossSectionRank(midnight, "k", "c1"); r != 1 {
		t.Fatalf("零点查询应命中盘中写入, got %d", r)
	}
}

// Clear 后必须查不到旧数据（防跨任务脏数据的关键防线）。
func TestCrossSection_Clear后不可查(t *testing.T) {
	ClearCrossSection()
	day := DayOf(time.Date(2025, 1, 6, 0, 0, 0, 0, time.Local))
	SetCrossSection(day, "k", []string{"c1", "c2"})
	ClearCrossSection()
	if r := CrossSectionRank(day, "k", "c1"); r != 0 {
		t.Fatalf("Clear 后名次应=0, got %d", r)
	}
}

// 防 lint：rand 引用占位（保持 import 稳定，供后续扩展随机并发写测试）
var _ = rand.Int
