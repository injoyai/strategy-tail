package lab

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/researchrun"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// matrix.go 横截面快照填充：A因子TopN 的名次上下文预计算。
//
// 数据口径与回测引擎逐日对齐：单票多年 series=his+dks，第 i 天的因子
// 前缀 series[:len(his)+i+1] 与引擎 Do() 的 ls := full[:len(his)+i+1]
// 完全一致（无前视）。NaN 视为无效跳过（不参与排名）；并列值按代码
// 字典序破平，名次确定可复现。快照仅进程内存（约 100MB/因子/年），
// 任务结束由调用方 ClearCrossSection。
//
// 加载失败的票无快照数据，A因子TopN 在无快照日期恒 false（安全退化）。
// day 必须从 K 线 Time 派生（core.DayOf(d.Dks[i].Time)）——core.DayOf
// 保留原 Location，time.Time 作 map 键按 Location 比较，独立构造的
// day 即使日历日相同也会静默 miss。

// fillCrossSection worker 池逐票计算因子值——本票 local map 累积、锁内
// 合并（共享 map 并发写会 race，镜像 run() 的 merged 模式）——结束后对
// 每个 (key, day) 排名写入 core.SetCrossSection。ctx 取消时返回
// ctx.Err() 且不写快照；数据加载失败票静默跳过（无快照 → TopN 恒 false）。
func fillCrossSection(ctx context.Context, codes []string, years []int, reqs []topNReq) error {
	if len(reqs) == 0 {
		return nil
	}
	var (
		mu     sync.Mutex
		merged []map[string]map[time.Time]map[string]float64 // key → day → code → 值
	)

	err := researchrun.ForEachCodeData(ctx, researchrun.Config{
		Codes:        codes,
		Years:        years,
		Workers:      common.DefaultGoroutines * 2, // 与 run() 同基线，快照填充并行度对齐回测
		DataMode:     researchrun.DailyClose,
		GetDayKlines: common.Pull.DayKlines,
	}, func(code string, datas []researchrun.YearData) {
		vals := map[string]map[time.Time]map[string]float64{}
		for _, d := range datas {
			series := make(extend.Klines, 0, len(d.His)+len(d.Dks))
			series = append(series, d.His...)
			series = append(series, d.Dks...)
			base := len(d.His)
			for i := range d.Dks {
				prefix := series[:base+i+1]
				day := core.DayOf(d.Dks[i].Time)
				for _, req := range reqs {
					v := req.factor.Value(code, prefix)
					if math.IsNaN(v) {
						continue
					}
					byDay := vals[req.key]
					if byDay == nil {
						byDay = map[time.Time]map[string]float64{}
						vals[req.key] = byDay
					}
					if byDay[day] == nil {
						byDay[day] = map[string]float64{}
					}
					byDay[day][code] = v
				}
			}
		}
		mu.Lock()
		merged = append(merged, vals)
		mu.Unlock()
	})
	if err != nil {
		return err
	}

	// 汇总排名（ForEachCodeData 返回后单协程执行，无竞争）
	for _, req := range reqs {
		byDay := map[time.Time]map[string]float64{}
		for _, m := range merged {
			for day, cv := range m[req.key] {
				if byDay[day] == nil {
					byDay[day] = map[string]float64{}
				}
				for c, v := range cv {
					byDay[day][c] = v
				}
			}
		}
		for day, cv := range byDay {
			type pair struct {
				val  float64
				code string
			}
			ps := make([]pair, 0, len(cv))
			for c, v := range cv {
				ps = append(ps, pair{val: v, code: c})
			}
			sort.Slice(ps, func(i, j int) bool {
				if ps[i].val != ps[j].val {
					if req.asc {
						return ps[i].val < ps[j].val
					}
					return ps[i].val > ps[j].val
				}
				return ps[i].code < ps[j].code
			})
			ranked := make([]string, len(ps))
			for i, p := range ps {
				ranked[i] = p.code
			}
			core.SetCrossSection(day, req.key, ranked)
		}
	}
	return nil
}
