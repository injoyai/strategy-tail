package buy

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

// P3 组迁移行为快照（台账附录 A：buy_price_above_ma / buy_volume_double /
// buy_volume_shrink / buy_rsi_low）。断言先按旧实现对拍固定，迁移到
// strategies/rule 内核后必须原样通过；helper 一律 p3 前缀，避免与并行
// 迁移组的同名符号冲突。

// p3Series 构造日 K 序列：open/high/low 取当日收盘价（本组四个组件均不
// 读取这三个字段），成交量为手；日期自 2026-01-01 起逐日递增。
func p3Series(closes []float64, volumes []int64) extend.Klines {
	if len(closes) != len(volumes) {
		panic("p3Series: closes 与 volumes 长度不一致")
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	ks := make(extend.Klines, len(closes))
	for i := range closes {
		ks[i] = &extend.Kline{Kline: &protocol.Kline{
			Time:   base.AddDate(0, 0, i),
			Open:   protocol.Yuan(closes[i]),
			High:   protocol.Yuan(closes[i]),
			Low:    protocol.Yuan(closes[i]),
			Close:  protocol.Yuan(closes[i]),
			Volume: volumes[i],
		}}
	}
	return ks
}

func p3ConstCloses(n int, close float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = close
	}
	return out
}

func p3ConstVolumes(n int, v int64) []int64 {
	out := make([]int64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

// TestP3现价高于均线快照 固定 A现价大于N日均线 的旧行为：
// 今收(浮点元) > N日均线(浮点元)，无 ma<=0 守卫，len(dks)<day 提前返回。
func TestP3现价高于均线快照(t *testing.T) {
	// 零值参数：day=0 → 组件内默认 20。19 根 10 元 + 末根 11 元，
	// MA20=10.05，今收高于均线应触发；末根 9 元（MA20=9.95）不触发。
	up20 := append(p3ConstCloses(19, 10), 11)
	down20 := append(p3ConstCloses(19, 10), 9)
	if !(A现价大于N日均线(0)).Buy("sh600000", p3Series(up20, p3ConstVolumes(20, 100))) {
		t.Fatal("零值参数：收盘 11 > MA20 10.05 应触发")
	}
	if (A现价大于N日均线(0)).Buy("sh600000", p3Series(down20, p3ConstVolumes(20, 100))) {
		t.Fatal("零值参数：收盘 9 < MA20 9.95 不应触发")
	}
	// 预热差一根：19 根不足以算 20 日均线。
	if (A现价大于N日均线(0)).Buy("sh600000", p3Series(up20[:19], p3ConstVolumes(19, 100))) {
		t.Fatal("预热差一根（19 根算 20 日均线）不应触发")
	}
	// 非默认周期 5，窗口恰满（len==day）。
	if !(A现价大于N日均线(5)).Buy("sh600000", p3Series([]float64{10, 10, 10, 10, 11}, p3ConstVolumes(5, 100))) {
		t.Fatal("period=5：收盘 11 > MA5 10.2 应触发")
	}
	if (A现价大于N日均线(5)).Buy("sh600000", p3Series([]float64{10, 10, 10, 10, 9}, p3ConstVolumes(5, 100))) {
		t.Fatal("period=5：收盘 9 < MA5 9.8 不应触发")
	}
	// 平价行情：今收 == 均线，严格大于不触发。
	if (A现价大于N日均线(5)).Buy("sh600000", p3Series(p3ConstCloses(5, 10), p3ConstVolumes(5, 100))) {
		t.Fatal("收盘等于均线（严格大于）不应触发")
	}
	// 空行情。
	if (A现价大于N日均线(5)).Buy("sh600000", nil) {
		t.Fatal("空行情不应触发")
	}
}

// TestP3倍量快照 固定 A倍量 的旧行为：今日量/前 base 日均量（不含今日）
// >= minRatio，且（maxRatio>0 且 maxRatio>minRatio 时）<= maxRatio；
// 基准均量 <=0 不触发。
func TestP3倍量快照(t *testing.T) {
	if !(A倍量{MinRatio: 1.1, BaseDays: 1}).Buy("sh600000",
		p3Series([]float64{10, 10}, []int64{50, 55})) {
		t.Fatal("55/50 恰等于下限 1.1，不能改用 50*1.1 比较而漏选")
	}
	if !(A倍量{MinRatio: 1, MaxRatio: 1.4, BaseDays: 1}).Buy("sh600000",
		p3Series([]float64{10, 10}, []int64{45, 63})) {
		t.Fatal("63/45 恰等于上限 1.4，不能改用 45*1.4 比较而漏选")
	}
	// 零值参数 == 目录默认 2/0/1：昨日量 100，今日 200 恰为 2 倍（下限含边界）。
	if !(A倍量{}).Buy("sh600000", p3Series([]float64{10, 10}, []int64{100, 200})) {
		t.Fatal("零值参数：今日量恰为昨日 2 倍应触发")
	}
	if (A倍量{}).Buy("sh600000", p3Series([]float64{10, 10}, []int64{100, 199})) {
		t.Fatal("零值参数：1.99 倍不足 2 倍不应触发")
	}
	// 非默认参数 1.5/0/5：前 5 日均量 100，今日 150 恰达下限。
	if !(A倍量{MinRatio: 1.5, BaseDays: 5}).Buy("sh600000",
		p3Series(p3ConstCloses(6, 10), []int64{100, 100, 100, 100, 100, 150})) {
		t.Fatal("1.5 倍/5 日基准：恰达下限应触发")
	}
	if (A倍量{MinRatio: 1.5, BaseDays: 5}).Buy("sh600000",
		p3Series(p3ConstCloses(6, 10), []int64{100, 100, 100, 100, 100, 149})) {
		t.Fatal("1.5 倍/5 日基准：不足下限不应触发")
	}
	// 上限生效（max>min）：恰等于上限仍在区间内，超出则排除。
	if !(A倍量{MinRatio: 1, MaxRatio: 2, BaseDays: 1}).Buy("sh600000",
		p3Series([]float64{10, 10}, []int64{100, 200})) {
		t.Fatal("恰等于上限 2 倍应触发（上限含边界）")
	}
	if (A倍量{MinRatio: 1, MaxRatio: 2, BaseDays: 1}).Buy("sh600000",
		p3Series([]float64{10, 10}, []int64{100, 201})) {
		t.Fatal("超出上限 2 倍不应触发")
	}
	// max==min / max<min：旧实现仅在 max>min 时应用上限。
	if !(A倍量{MinRatio: 2, MaxRatio: 2, BaseDays: 1}).Buy("sh600000",
		p3Series([]float64{10, 10}, []int64{100, 500})) {
		t.Fatal("max==min 时上限不生效，5 倍应触发")
	}
	if !(A倍量{MinRatio: 3, MaxRatio: 1, BaseDays: 1}).Buy("sh600000",
		p3Series([]float64{10, 10}, []int64{100, 400})) {
		t.Fatal("max<min 时上限不生效，4 倍应触发")
	}
	// 预热差一根：base=5 需 6 根。
	if (A倍量{BaseDays: 5}).Buy("sh600000", p3Series(p3ConstCloses(5, 10), p3ConstVolumes(5, 100))) {
		t.Fatal("base=5 仅 5 根（预热差一根）不应触发")
	}
	// 基准均量为 0：无基准不触发。
	if (A倍量{}).Buy("sh600000", p3Series([]float64{10, 10}, []int64{0, 100})) {
		t.Fatal("昨日量为 0（无基准）不应触发")
	}
	// 今日零量：0 倍不达下限。
	if (A倍量{}).Buy("sh600000", p3Series([]float64{10, 10}, []int64{100, 0})) {
		t.Fatal("今日零量不应触发")
	}
	// 空行情 / 仅一根。
	if (A倍量{}).Buy("sh600000", nil) {
		t.Fatal("空行情不应触发")
	}
	if (A倍量{}).Buy("sh600000", p3Series([]float64{10}, []int64{100})) {
		t.Fatal("仅一根（无昨日基准）不应触发")
	}
}

// TestP3缩量快照 固定 A缩量 的旧行为：今日量 < 前 Days 日均量（不含今日）
// ×Ratio；均量 <=0 或缺数据不触发。
func TestP3缩量快照(t *testing.T) {
	// 零值参数 == 目录默认 5/0.8：前 5 日均量 100。
	if !(A缩量{}).Buy("sh600000", p3Series(p3ConstCloses(6, 10), append(p3ConstVolumes(5, 100), 79))) {
		t.Fatal("零值参数：今日量 79 < 100×0.8 应触发")
	}
	if (A缩量{}).Buy("sh600000", p3Series(p3ConstCloses(6, 10), append(p3ConstVolumes(5, 100), 80))) {
		t.Fatal("今日量恰等于 100×0.8（严格小于）不应触发")
	}
	if (A缩量{}).Buy("sh600000", p3Series(p3ConstCloses(6, 10), append(p3ConstVolumes(5, 100), 81))) {
		t.Fatal("今日量 81 高于 80 不应触发")
	}
	// 非默认参数 1/0.6。
	if !(A缩量{Days: 1, Ratio: 0.6}).Buy("sh600000", p3Series([]float64{10, 10}, []int64{100, 59})) {
		t.Fatal("days=1/ratio=0.6：59 < 60 应触发")
	}
	if (A缩量{Days: 1, Ratio: 0.6}).Buy("sh600000", p3Series([]float64{10, 10}, []int64{100, 60})) {
		t.Fatal("恰等于上限 60（严格小于）不应触发")
	}
	// 预热差一根：days=5 需 6 根。
	if (A缩量{}).Buy("sh600000", p3Series(p3ConstCloses(5, 10), p3ConstVolumes(5, 100))) {
		t.Fatal("days=5 仅 5 根（预热差一根）不应触发")
	}
	// 前期均量为 0：无基准不触发。
	if (A缩量{}).Buy("sh600000", p3Series(p3ConstCloses(6, 10), append(p3ConstVolumes(5, 0), 50))) {
		t.Fatal("均量为 0（无基准）不应触发")
	}
	// 今日零量视为极限缩量。
	if !(A缩量{}).Buy("sh600000", p3Series(p3ConstCloses(6, 10), append(p3ConstVolumes(5, 100), 0))) {
		t.Fatal("今日零量应视为缩量触发")
	}
	// 空行情。
	if (A缩量{}).Buy("sh600000", nil) {
		t.Fatal("空行情不应触发")
	}
}

// TestP3RSI快照 固定 RSI 的旧行为：RSI(浮点) < threshold（严格），
// len(dks)<period+1 提前返回（该窗口内因子返回 NaN，不会被求值）。
func TestP3RSI快照(t *testing.T) {
	// 零值参数 == 目录默认 14/20：14 根 10 元 + 末根 9 元 → RSI=0。
	if !(RSI{}).Buy("sh600000", p3Series(append(p3ConstCloses(14, 10), 9), p3ConstVolumes(15, 100))) {
		t.Fatal("零值参数：末根下跌 RSI=0 < 20 应触发")
	}
	// 平价行情：全 0 变动 → RSI=50，不触发。
	if (RSI{}).Buy("sh600000", p3Series(p3ConstCloses(15, 10), p3ConstVolumes(15, 100))) {
		t.Fatal("平价行情 RSI=50 不应触发")
	}
	// 末根上涨：无亏损 → RSI=100，不触发。
	if (RSI{}).Buy("sh600000", p3Series(append(p3ConstCloses(14, 10), 11), p3ConstVolumes(15, 100))) {
		t.Fatal("末根上涨 RSI=100 不应触发")
	}
	// 非默认参数 5/30。
	if !(RSI{Period: 5, Threshold: 30}).Buy("sh600000",
		p3Series(append(p3ConstCloses(5, 10), 9), p3ConstVolumes(6, 100))) {
		t.Fatal("period=5/threshold=30：RSI=0 应触发")
	}
	if (RSI{Period: 5, Threshold: 30}).Buy("sh600000",
		p3Series(p3ConstCloses(6, 10), p3ConstVolumes(6, 100))) {
		t.Fatal("period=5/threshold=30：RSI=50 不应触发")
	}
	// 阈值 1/99 边界（period=1 时 RSI 只会取 0/50/100）。
	if !(RSI{Period: 1, Threshold: 1}).Buy("sh600000", p3Series([]float64{10, 9}, []int64{100, 100})) {
		t.Fatal("RSI=0 < 阈值 1 应触发")
	}
	if (RSI{Period: 1, Threshold: 1}).Buy("sh600000", p3Series([]float64{10, 10}, []int64{100, 100})) {
		t.Fatal("RSI=50 不低于阈值 1 不应触发")
	}
	if (RSI{Period: 1, Threshold: 99}).Buy("sh600000", p3Series([]float64{10, 11}, []int64{100, 100})) {
		t.Fatal("RSI=100 不低于阈值 99 不应触发")
	}
	if !(RSI{Period: 1, Threshold: 99}).Buy("sh600000", p3Series([]float64{10, 9}, []int64{100, 100})) {
		t.Fatal("RSI=0 < 阈值 99 应触发")
	}
	// 阈值相等：RSI==threshold（严格小于）不触发。
	if (RSI{Period: 1, Threshold: 50}).Buy("sh600000", p3Series([]float64{10, 10}, []int64{100, 100})) {
		t.Fatal("RSI==阈值（严格小于）不应触发")
	}
	// 预热差一根：period=14 需 15 根。
	if (RSI{}).Buy("sh600000", p3Series(p3ConstCloses(14, 10), p3ConstVolumes(14, 100))) {
		t.Fatal("预热差一根（14 根算 14 日 RSI）不应触发")
	}
	// 空行情。
	if (RSI{}).Buy("sh600000", nil) {
		t.Fatal("空行情不应触发")
	}
}
