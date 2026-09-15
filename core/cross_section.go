package core

import (
	"sync"
	"time"
)

// cross_section.go 横截面快照：A因子TopN 的排名上下文。
//
// A因子TopN.Buy() 是单股视角，看不到当日其他股票的因子值，排名由回测/分析
// 入口预计算后注入：先按因子+方向（TopNKey）收集需求 → 单遍算值 → 排名 →
// SetCrossSection 写入本快照 → 回测主循环 O(1) 查询 → 任务结束 ClearCrossSection。
//
// key=因子名（含参数，如 "N日动量(20)"）+方向：不同因子的快照互不覆盖；
// 同一因子多买方复用只算一次。仅内存，不落库（spec §6 边界）。

var (
	csMu    sync.RWMutex
	csStore = map[string]map[time.Time]map[string]int{} // key → day → code → 名次(1起)
)

// TopNKey 生成快照 key：因子名 + 方向，保证多变体多方向互不覆盖。
func TopNKey(factorName string, asc bool) string {
	if asc {
		return factorName + "|asc"
	}
	return factorName + "|desc"
}

// SetCrossSection 写入某日某 key 的排名快照（ranked[0] 为第 1 名），同 key 整体覆盖。
func SetCrossSection(day time.Time, key string, ranked []string) {
	day = DayOf(day)
	ranks := make(map[string]int, len(ranked))
	for i, code := range ranked {
		ranks[code] = i + 1
	}
	csMu.Lock()
	defer csMu.Unlock()
	byDay, ok := csStore[key]
	if !ok {
		byDay = map[time.Time]map[string]int{}
		csStore[key] = byDay
	}
	byDay[day] = ranks
}

// CrossSectionRank 查询某日某 key 下 code 的名次（1 起）；未命中返回 0。
func CrossSectionRank(day time.Time, key, code string) int {
	day = DayOf(day)
	csMu.RLock()
	defer csMu.RUnlock()
	byDay, ok := csStore[key]
	if !ok {
		return 0
	}
	ranks, ok := byDay[day]
	if !ok {
		return 0
	}
	return ranks[code]
}

// ClearCrossSection 清空全部快照（任务结束调用，防跨任务脏数据）。
func ClearCrossSection() {
	csMu.Lock()
	defer csMu.Unlock()
	csStore = map[string]map[time.Time]map[string]int{}
}
