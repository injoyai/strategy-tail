package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
)

// 独立数据诊断脚本：检查各年份实际数据覆盖范围，不联网
func main() {
	dataDir := "data/database"
	pattern := filepath.Join(dataDir, "day-kline", "*.db")
	matches, _ := filepath.Glob(pattern)

	// 收集沪深主板代码（文件名格式: sh600519.db / sz000001.db）
	codes := []string{}
	for _, m := range matches {
		name := filepath.Base(m)
		name = name[:len(name)-3] // 去掉 .db
		if len(name) != 8 {
			continue
		}
		if name[:4] == "sh60" || name[:4] == "sz00" {
			codes = append(codes, name)
		}
	}
	sort.Strings(codes)
	fmt.Printf("沪深主板代码数: %d\n", len(codes))
	if len(codes) == 0 {
		return
	}

	pull, err := extend.NewPullKline(extend.PullKlineConfig{
		Types: []string{extend.Day},
		Dir:   dataDir,
	})
	if err != nil {
		fmt.Println("初始化失败:", err)
		return
	}

	// 抽样检查：第一个、中间、最后一个代码的完整数据范围
	samples := []string{codes[0], codes[len(codes)/2], codes[len(codes)-1]}
	// 再加几个知名大盘股
	for _, c := range codes {
		if c == "sh600519" || c == "sz000001" || c == "sh600036" {
			samples = append(samples, c)
		}
	}

	fmt.Println("\n=== 样本代码数据范围 ===")
	for _, code := range samples {
		dks, err := pull.DayKlines(code, time.Time{}, time.Now())
		if err != nil || len(dks) == 0 {
			fmt.Printf("%s: 无数据 (%v)\n", code, err)
			continue
		}
		first := dks[0].Time
		last := dks[len(dks)-1].Time
		fmt.Printf("%s: %s ~ %s, 共 %d 根\n", code, first.Format("2006-01-02"), last.Format("2006-01-02"), len(dks))
	}

	// 统计所有代码 2026 年的数据量分布
	fmt.Println("\n=== 2026年数据覆盖率统计 ===")
	year2026Start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	has2026 := 0
	no2026 := 0
	max2026Date := time.Time{}
	buckets := map[int]int{} // 按月份统计最后数据日
	for _, code := range codes {
		dks, err := pull.DayKlines(code, time.Time{}, time.Now())
		if err != nil || len(dks) == 0 {
			no2026++
			continue
		}
		last := dks[len(dks)-1].Time
		if last.After(year2026Start) {
			has2026++
			m := int(last.Month())
			buckets[m]++
			if last.After(max2026Date) {
				max2026Date = last
			}
		} else {
			no2026++
		}
	}
	fmt.Printf("有2026年数据的代码: %d / %d\n", has2026, has2026+no2026)
	fmt.Printf("无2026年数据的代码: %d\n", no2026)
	fmt.Printf("最新数据日: %s\n", max2026Date.Format("2006-01-02"))
	fmt.Println("按最后数据日月份分布:")
	for m := 1; m <= 12; m++ {
		if buckets[m] > 0 {
			fmt.Printf("  %d月: %d 只\n", m, buckets[m])
		}
	}

	// 统计各年数据量
	fmt.Println("\n=== 各年度数据量统计（所有代码） ===")
	yearCounts := map[int]int{}
	for _, code := range codes {
		dks, _ := pull.DayKlines(code, time.Time{}, time.Now())
		seen := map[int]bool{}
		for _, k := range dks {
			y := k.Time.Year()
			if !seen[y] {
				seen[y] = true
				yearCounts[y]++
			}
		}
	}
	for y := 2020; y <= 2026; y++ {
		fmt.Printf("  %d年: %d 只代码有数据\n", y, yearCounts[y])
	}
}
