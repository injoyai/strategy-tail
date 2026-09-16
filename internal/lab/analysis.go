package lab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/injoyai/goutil/oss"
	"github.com/injoyai/goutil/oss/csv"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/researchrun"
	"github.com/injoyai/strategy-tail/lib/extend"
	f "github.com/injoyai/strategy-tail/strategies/factor"
)

// analysis.go 因子研究：统计层（Task 12）与编排导出（Task 13）。
//
// 统计全部纯函数无 IO。单期 IC 用 Spearman 秩相关——对量纲不敏感，
// 只关心截面单调性；样本不足 minPairs 时汇总归零（无推断意义）。

// minPairs IC 汇总的最小有效样本数。
const minPairs = 10

// ICStats 一组逐期 IC 的汇总统计。
type ICStats struct {
	Pairs int     `json:"pairs"` // 有效样本数
	Mean  float64 `json:"mean"`  // 均值
	Std   float64 `json:"std"`   // 总体标准差
	TStat float64 `json:"tStat"` // Mean/(Std/√n)；Std=0 时 0
}

// avgRanks 升序平均名次（值最小名次 1，并列取均值），Spearman 基础。
func avgRanks(xs []float64) []float64 {
	n := len(xs)
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(i, j int) bool { return xs[idx[i]] < xs[idx[j]] })
	ranks := make([]float64, n)
	for i := 0; i < n; {
		j := i
		for j+1 < n && xs[idx[j+1]] == xs[idx[i]] {
			j++
		}
		avg := float64(i+j+2) / 2
		for k := i; k <= j; k++ {
			ranks[idx[k]] = avg
		}
		i = j + 1
	}
	return ranks
}

// spearmanIC 单期 IC：因子值名次与收益名次的 Pearson 相关。
// NaN 由调用方剔除（Pearson 遇 NaN 结果不可用）。
func spearmanIC(vals, rets []float64) float64 {
	return f.Pearson(avgRanks(vals), avgRanks(rets))
}

// quintileMeans 按因子值升序等频五分位（Q1=因子最低 20%），返回各组
// 收益均值；n<5 返回 nil。vals 与 rets 须等长且无 NaN，由调用方保证
// （NaN 会经组均值传播，不 panic）。
func quintileMeans(vals, rets []float64) []float64 {
	n := len(vals)
	if n < 5 {
		return nil
	}
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(i, j int) bool { return vals[idx[i]] < vals[idx[j]] })
	sums := make([]float64, 5)
	cnts := make([]int, 5)
	for k, i := range idx {
		q := k * 5 / n
		sums[q] += rets[i]
		cnts[q]++
	}
	out := make([]float64, 5)
	for q := range out {
		out[q] = sums[q] / float64(cnts[q])
	}
	return out
}

// icStats 汇总逐期 IC：剔除 NaN，有效样本 < minPairs 时全 0。
func icStats(ics []float64) ICStats {
	valid := make([]float64, 0, len(ics))
	for _, v := range ics {
		if !math.IsNaN(v) {
			valid = append(valid, v)
		}
	}
	var s ICStats
	s.Pairs = len(valid)
	if s.Pairs < minPairs {
		return s
	}
	sum := 0.0
	for _, v := range valid {
		sum += v
	}
	mean := sum / float64(s.Pairs)
	ss := 0.0
	for _, v := range valid {
		ss += (v - mean) * (v - mean)
	}
	s.Mean = mean
	s.Std = math.Sqrt(ss / float64(s.Pairs))
	if s.Std > 0 {
		s.TStat = s.Mean / (s.Std / math.Sqrt(float64(s.Pairs)))
	}
	return s
}

// AnalyzeConfig 因子分析配置：内嵌 RunConfig 复用年份/样本解析；
// 卖出规则与分析无关（不校验）。
type AnalyzeConfig struct {
	RunConfig
	Kind   string `json:"kind"`   // 因子类型（registry kind）
	Days   int    `json:"days"`   // 因子窗口参数（≤0 用各因子默认）
	Window int    `json:"window"` // 未来收益窗口（交易日）
}

// Validate 校验分析配置（复用回测的年份/样本检查）。
func (c AnalyzeConfig) Validate() error {
	if err := c.validateYears(); err != nil {
		return err
	}
	if err := c.validateSample(); err != nil {
		return err
	}
	if c.Window < 1 || c.Window > 60 {
		return fmt.Errorf("未来收益窗口无效: %d（应为 1-60 交易日）", c.Window)
	}
	if f.Build(c.Kind, c.Days) == nil {
		return fmt.Errorf("未知因子类型: %s", c.Kind)
	}
	return nil
}

// AnalysisReport 因子分析报告。
type AnalysisReport struct {
	FactorName string    `json:"factorName"` // 因子中文名（如 N日动量(2)）
	Kind       string    `json:"kind"`
	Window     int       `json:"window"`
	Stats      ICStats   `json:"stats"`
	Quintiles  []float64 `json:"quintiles"` // 逐日五分位收益均值的再平均；无有效日为 nil
	Daily      []DailyIC `json:"daily"`
	StartedAt  string    `json:"startedAt"`
	FinishedAt string    `json:"finishedAt"`
}

// DailyIC 单日横截面 IC；IC=nil 表示当日无效（NaN，JSON 序列化为 null）。
type DailyIC struct {
	Date string   `json:"date"`
	IC   *float64 `json:"ic"`
}

// runAnalysis 因子分析主循环：逐日横截面 因子值名次 vs 未来收益名次 → Spearman IC。
// 口径与 fillCrossSection 一致：series=his+dks，前缀无前视；未来收益
// dks[i+Window].Close/dks[i].Close−1，尾部 Window 日无未来收益剔除。
func (r *Runner) runAnalysis(cfg AnalyzeConfig, stop chan struct{}) (*AnalysisReport, error) {
	started := time.Now()

	codes, err := r.resolveCodes(cfg.RunConfig, stop)
	if err != nil {
		return nil, err
	}
	r.totalCodes.Store(int64(len(codes)))
	years := cfg.RunConfig.years()
	fct := f.Build(cfg.Kind, cfg.Days)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-stop:
			cancel()
		case <-ctx.Done():
		}
	}()

	var mu sync.Mutex
	var mergedV, mergedR []map[time.Time]map[string]float64 // day → code → 值/收益

	if err := researchrun.ForEachCodeData(ctx, researchrun.Config{
		Codes:        codes,
		Years:        years,
		Workers:      common.DefaultGoroutines * 2,
		DataMode:     researchrun.DailyClose,
		GetDayKlines: common.Pull.DayKlines,
		OnCodeDone: func(progress researchrun.Progress) {
			r.doneCodes.Store(int64(progress.Done))
			current := progress.Code
			r.currentCode.Store(&current)
		},
	}, func(code string, datas []researchrun.YearData) {
		lv := map[time.Time]map[string]float64{}
		lr := map[time.Time]map[string]float64{}
		for _, d := range datas {
			series := make(extend.Klines, 0, len(d.His)+len(d.Dks))
			series = append(series, d.His...)
			series = append(series, d.Dks...)
			base := len(d.His)
			for i := 0; i+cfg.Window < len(d.Dks); i++ {
				v := fct.Value(code, series[:base+i+1])
				ret := d.Dks[i+cfg.Window].Close.Float64()/d.Dks[i].Close.Float64() - 1
				// NaN/±Inf 值与该票该日一并剔除：spearmanIC/quintileMeans 契约要求入参无 NaN，
				// 且 Inf 会传入 Quintiles 使 report.json 序列化失败（退化数据如收盘价 0）
				if math.IsNaN(v) || math.IsInf(v, 0) || math.IsNaN(ret) || math.IsInf(ret, 0) {
					continue
				}
				day := core.DayOf(d.Dks[i].Time)
				if lv[day] == nil {
					lv[day] = map[string]float64{}
					lr[day] = map[string]float64{}
				}
				lv[day][code] = v
				lr[day][code] = ret
			}
		}
		mu.Lock()
		mergedV = append(mergedV, lv)
		mergedR = append(mergedR, lr)
		mu.Unlock()
	}); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, errStopped
		}
		return nil, err
	}

	select {
	case <-stop:
		return nil, errStopped
	default:
	}

	// 合并各票数据（单协程，无竞争）
	vals := map[time.Time]map[string]float64{}
	rets := map[time.Time]map[string]float64{}
	for i, m := range mergedV {
		for day, cv := range m {
			if vals[day] == nil {
				vals[day] = map[string]float64{}
				rets[day] = map[string]float64{}
			}
			for c, v := range cv {
				vals[day][c] = v
				rets[day][c] = mergedR[i][day][c]
			}
		}
	}

	// 逐日计算 IC（日期升序输出）
	days := make([]time.Time, 0, len(vals))
	for day := range vals {
		days = append(days, day)
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Before(days[j]) })

	daily := make([]DailyIC, 0, len(days))
	ics := make([]float64, 0, len(days))
	qSums, qCnts := make([]float64, 5), make([]int, 5)
	for _, day := range days {
		vs := make([]float64, 0, len(vals[day]))
		rs := make([]float64, 0, len(vals[day]))
		for c, v := range vals[day] {
			vs = append(vs, v)
			rs = append(rs, rets[day][c])
		}
		di := DailyIC{Date: day.Format("2006-01-02")}
		ic := spearmanIC(vs, rs)
		if !math.IsNaN(ic) {
			ics = append(ics, ic)
			v := ic
			di.IC = &v
			// 逐日五分位均值再平均（每日等权，与 IC 口径一致）
			if qs := quintileMeans(vs, rs); qs != nil {
				for q := range qs {
					qSums[q] += qs[q]
					qCnts[q]++
				}
			}
		}
		daily = append(daily, di)
	}

	rep := &AnalysisReport{
		FactorName: fct.Name(),
		Kind:       cfg.Kind,
		Window:     cfg.Window,
		Stats:      icStats(ics),
		Daily:      daily,
		StartedAt:  started.Format(time.RFC3339),
		FinishedAt: time.Now().Format(time.RFC3339),
	}
	if qCnts[0] > 0 { // 至少有一天票数 ≥5 才有意义（防 0 除 NaN 炸 JSON）
		rep.Quintiles = make([]float64, 5)
		for q := range qSums {
			rep.Quintiles[q] = qSums[q] / float64(qCnts[q])
		}
	}
	if err := exportAnalysis(rep); err != nil {
		return nil, fmt.Errorf("分析报告落盘失败: %w", err)
	}
	return rep, nil
}

// analysisHTML 分析报告页模板（ECharts 逐日 IC 折线；%% 为 CSS 转义）。
const analysisHTML = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>%s IC 分析</title>
<script src="https://cdn.jsdelivr.net/npm/echarts@5/dist/echarts.min.js"></script>
</head>
<body>
<h2>%s · 未来 %d 交易日 IC</h2>
<p>均值 %.4f · 标准差 %.4f · t 值 %.2f · 有效样本 %d 日</p>
<div id="chart" style="width:100%%;height:420px"></div>
<script>
const days = %s, ics = %s;
echarts.init(document.getElementById('chart')).setOption({
  tooltip: {trigger: 'axis'},
  xAxis: {type: 'category', data: days},
  yAxis: {type: 'value', min: -1, max: 1},
  series: [{type: 'line', data: ics, connectNulls: true, markLine: {data: [{yAxis: 0}]}}]
});
</script>
</body>
</html>`

// exportAnalysis 落盘分析产物到 output/factor/<清洗后kind>/：
// report.json（tmp+rename 原子写，镜像 saveReport）+ ic.csv + report.html
// （后两者 best-effort，不阻塞主产物）。
func exportAnalysis(rep *AnalysisReport) error {
	dir := filepath.Join("output", "factor", core.TradesExportName(rep.Kind))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	buf, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, ".report.json.tmp")
	if err := os.WriteFile(tmp, buf, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, "report.json")); err != nil {
		return err
	}

	// ic.csv（无效日按 0 写）
	rows := [][]any{{"date", "ic"}}
	for _, d := range rep.Daily {
		v := 0.0
		if d.IC != nil {
			v = *d.IC
		}
		rows = append(rows, []any{d.Date, v})
	}
	if buf, err := csv.Export(rows); err == nil {
		_ = oss.New(filepath.Join(dir, "ic.csv"), buf)
	}

	// report.html（无效日为 null，前端 connectNulls 补线）
	days := make([]string, 0, len(rep.Daily))
	ics := make([]*float64, 0, len(rep.Daily))
	for _, d := range rep.Daily {
		days = append(days, d.Date)
		ics = append(ics, d.IC)
	}
	dayJSON, _ := json.Marshal(days)
	icJSON, _ := json.Marshal(ics)
	html := fmt.Sprintf(analysisHTML, rep.FactorName, rep.FactorName, rep.Window,
		rep.Stats.Mean, rep.Stats.Std, rep.Stats.TStat, rep.Stats.Pairs, dayJSON, icJSON)
	_ = os.WriteFile(filepath.Join(dir, "report.html"), []byte(html), 0644)
	return nil
}
