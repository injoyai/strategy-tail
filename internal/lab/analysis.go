package lab

import (
	"math"
	"sort"

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
