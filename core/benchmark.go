package core

import (
	"sort"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
)

// BenchmarkReturn 计算基准（指数/ETF）在给定日线区间的收益率。
// 公式：末收盘价 / 首收盘价 - 1。
// 数据不足（少于 2 根）返回 0。
func BenchmarkReturn(dks extend.Klines) float64 {
	if len(dks) < 2 {
		return 0
	}
	first := dks[0].Close.Float64()
	last := dks[len(dks)-1].Close.Float64()
	if first <= 0 {
		return 0
	}
	return last/first - 1
}

// AlphaBeta 通过 CAPM 模型计算策略相对基准的 Alpha 和 Beta。
// strat/bench 为同期、等长的收益率序列（小数）。
// Beta = Cov(strat, bench) / Var(bench)
// Alpha = mean(strat) - Beta * mean(bench)
// 长度不一致或基准无波动返回 0, 0。
func AlphaBeta(strat, bench []float64) (alpha, beta float64) {
	n := len(strat)
	if n < 2 || n != len(bench) {
		return 0, 0
	}
	meanS := 0.0
	meanB := 0.0
	for i := 0; i < n; i++ {
		meanS += strat[i]
		meanB += bench[i]
	}
	meanS /= float64(n)
	meanB /= float64(n)

	var cov, varB float64
	for i := 0; i < n; i++ {
		ds := strat[i] - meanS
		db := bench[i] - meanB
		cov += ds * db
		varB += db * db
	}
	if varB == 0 {
		return 0, 0
	}
	beta = cov / varB
	alpha = meanS - beta*meanB
	return alpha, beta
}

// computeTradeAlphaBeta 将每笔交易收益率与同期基准日收益率对齐后计算 Alpha/Beta。
// 基准日线按交易卖出日对齐；若基准数据不足或无法对齐返回 0, 0。
func computeTradeAlphaBeta(trades []Trade, benchmarkKlines extend.Klines) (alpha, beta float64) {
	if len(trades) < 2 || len(benchmarkKlines) < 2 {
		return 0, 0
	}

	// 按卖出日时间索引构建基准日线查找表
	sortedKlines := make(extend.Klines, len(benchmarkKlines))
	copy(sortedKlines, benchmarkKlines)
	sort.Slice(sortedKlines, func(i, j int) bool {
		return sortedKlines[i].Time.Before(sortedKlines[j].Time)
	})

	// 按日期索引基准收盘价
	dateClose := make(map[string]float64, len(sortedKlines))
	for _, k := range sortedKlines {
		dateClose[k.Time.Format(time.DateOnly)] = k.Close.Float64()
	}

	stratReturns := make([]float64, 0, len(trades))
	benchReturns := make([]float64, 0, len(trades))
	for _, t := range trades {
		buy := t.BuyPrice.Float64()
		if buy <= 0 {
			continue
		}
		stratR := (t.SellPrice.Float64() - buy) / buy
		// 基准收益：买入日到卖出日的区间收益
		buyDate := t.BuyTime.Format(time.DateOnly)
		sellDate := t.SellTime.Format(time.DateOnly)
		buyClose, ok1 := dateClose[buyDate]
		sellClose, ok2 := dateClose[sellDate]
		if !ok1 || !ok2 || buyClose <= 0 {
			continue
		}
		benchR := sellClose/buyClose - 1
		stratReturns = append(stratReturns, stratR)
		benchReturns = append(benchReturns, benchR)
	}

	return AlphaBeta(stratReturns, benchReturns)
}
