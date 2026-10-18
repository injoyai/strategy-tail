package buy

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

// P4 组迁移行为快照（台账附录 A：buy_close_above_ma / buy_volume_surge /
// buy_rsi_range / buy_turnover_min / buy_atr_range / buy_break_high /
// buy_ma_bull / buy_solid_bull，旧实现集中在 buy_trend_volume.go）。
// 断言先按旧实现对拍固定，迁移到 strategies/rule 共用内核后本文件必须
// 原样通过；helper 一律 p4 前缀，避免与其他并行迁移组冲突。

// p4Bar 构造单根 K 线：open/high/low/close 与成交额 amount 一律给厘
// （protocol.Price，Kline.Amount 即厘口径），volume 为手数；last（昨收）
// 本组组件不读取，固定与 close 相同。
func p4Bar(open, high, low, close protocol.Price, volume int64, amount protocol.Price) *extend.Kline {
	return &extend.Kline{Kline: &protocol.Kline{
		Last:   close,
		Open:   open,
		High:   high,
		Low:    low,
		Close:  close,
		Volume: volume,
		Amount: amount,
	}}
}

// p4Ks 组装 K 线前缀，日期自 2026-01-01 起逐日递增。
func p4Ks(bars ...*extend.Kline) extend.Klines {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	for i, b := range bars {
		b.Time = base.AddDate(0, 0, i)
		b.Unix = b.Time.Unix()
	}
	return bars
}

// p4Flat 构造 n 根 open=high=low=close 的平价 K 线。
func p4Flat(n int, close protocol.Price, volume int64, amount protocol.Price) extend.Klines {
	bars := make([]*extend.Kline, n)
	for i := range bars {
		bars[i] = p4Bar(close, close, close, close, volume, amount)
	}
	return p4Ks(bars...)
}

// p4Volumes 覆盖前缀中各根的成交量（手）。
func p4Volumes(ks extend.Klines, vols ...int64) extend.Klines {
	for i, v := range vols {
		ks[i].Volume = v
	}
	return ks
}

// ---- A收盘高于均线（buy_close_above_ma）----
// 旧实现：Period 0→20；len(dks)<period → false；今收(浮点元) > N日均线
//（extend 整数厘均线，截断平均）严格大于。

// TestP4收盘高于均线快照 覆盖零值参数、预热差一根、非默认周期、阈值相等、
// 平价/零量行情与空行情。
func TestP4收盘高于均线快照(t *testing.T) {
	// 零值参数：前 19 根 10 元 + 末根 11 元，MA20 = 201000/20 = 10.05 元。
	up := append(p4Flat(19, 10000, 100, 1e8), p4Bar(11000, 11000, 11000, 11000, 100, 1e8))
	if !(A收盘高于均线{}).Buy("sh600000", up) {
		t.Fatal("零值参数：今收 11 元 > MA20 10.05 元应触发")
	}
	down := append(p4Flat(19, 10000, 100, 1e8), p4Bar(9000, 9000, 9000, 9000, 100, 1e8))
	if (A收盘高于均线{}).Buy("sh600000", down) {
		t.Fatal("零值参数：今收 9 元 < MA20 9.95 元不应触发")
	}
	// 预热差一根：19 根算 20 日均线。
	if (A收盘高于均线{}).Buy("sh600000", p4Flat(19, 10000, 100, 1e8)) {
		t.Fatal("预热差一根（19 根算 20 日均线）不应触发")
	}
	// 非默认周期 5，窗口恰满。
	fiveUp := append(p4Flat(4, 10000, 100, 1e8), p4Bar(11000, 11000, 11000, 11000, 100, 1e8))
	if !(A收盘高于均线{Period: 5}).Buy("sh600000", fiveUp) {
		t.Fatal("period=5：今收 11 > MA5 10.2 应触发")
	}
	fiveDown := append(p4Flat(4, 10000, 100, 1e8), p4Bar(9000, 9000, 9000, 9000, 100, 1e8))
	if (A收盘高于均线{Period: 5}).Buy("sh600000", fiveDown) {
		t.Fatal("period=5：今收 9 < MA5 9.8 不应触发")
	}
	// 阈值相等/平价/零量行情：5 根全 10 元且量为 0，今收 == 均线（严格大于）。
	if (A收盘高于均线{Period: 5}).Buy("sh600000", p4Flat(5, 10000, 0, 1e8)) {
		t.Fatal("平价零量行情今收等于均线（严格大于）不应触发")
	}
	// 空行情。
	if (A收盘高于均线{}).Buy("sh600000", nil) {
		t.Fatal("空行情不应触发")
	}
}

// ---- A成交量放大（buy_volume_surge）----
// 旧实现：Period 0→5、Ratio 0→1.5；len<period+1 → false；今日量(手) >
// 前 period 日均量(不含今日)×ratio 严格大于；基准均量 <=0 → false。

// TestP4成交量放大快照 覆盖零值参数、非默认值、阈值相等、预热差一根、
// 放量/缩量方向、基准均量为 0、今日零量与空行情。
func TestP4成交量放大快照(t *testing.T) {
	// 零值参数：前 5 日量各 100，今日 150 恰为 1.5 倍（严格大于不触发）。
	if (A成交量放大{}).Buy("sh600000", p4Volumes(p4Flat(6, 10000, 100, 1e8), 100, 100, 100, 100, 100, 150)) {
		t.Fatal("今日量恰为 1.5 倍均量（严格大于）不应触发")
	}
	if !(A成交量放大{}).Buy("sh600000", p4Volumes(p4Flat(6, 10000, 100, 1e8), 100, 100, 100, 100, 100, 151)) {
		t.Fatal("今日量 151 > 150 应触发")
	}
	if (A成交量放大{}).Buy("sh600000", p4Volumes(p4Flat(6, 10000, 100, 1e8), 100, 100, 100, 100, 100, 100)) {
		t.Fatal("平量（缩量方向）不应触发")
	}
	// 非默认参数 1 日基准 2 倍：今日 200 恰等阈值不触发，201 触发。
	if (A成交量放大{Period: 1, Ratio: 2}).Buy("sh600000", p4Volumes(p4Flat(2, 10000, 100, 1e8), 100, 200)) {
		t.Fatal("今日量恰为 2 倍昨日量（阈值相等）不应触发")
	}
	if !(A成交量放大{Period: 1, Ratio: 2}).Buy("sh600000", p4Volumes(p4Flat(2, 10000, 100, 1e8), 100, 201)) {
		t.Fatal("今日量 201 > 200 应触发")
	}
	// 预热差一根：period=5 需 6 根。
	if (A成交量放大{}).Buy("sh600000", p4Flat(5, 10000, 100, 1e8)) {
		t.Fatal("预热差一根（5 根算 5 日基准）不应触发")
	}
	// 基准均量为 0：无基准不触发。
	if (A成交量放大{}).Buy("sh600000", p4Volumes(p4Flat(6, 10000, 100, 1e8), 0, 0, 0, 0, 0, 100)) {
		t.Fatal("基准均量为 0 不应触发")
	}
	// 今日零量：0 > 正阈值不成立。
	if (A成交量放大{}).Buy("sh600000", p4Volumes(p4Flat(6, 10000, 100, 1e8), 100, 100, 100, 100, 100, 0)) {
		t.Fatal("今日零量不应触发")
	}
	// 空行情 / 仅一根。
	if (A成交量放大{}).Buy("sh600000", nil) {
		t.Fatal("空行情不应触发")
	}
	if (A成交量放大{}).Buy("sh600000", p4Flat(1, 10000, 100, 1e8)) {
		t.Fatal("仅一根（无昨日基准）不应触发")
	}
}

// ---- RSI区间（buy_rsi_range）----
// 旧实现：Period 0→14、Min 0→30、Max 0→50；len<period+1 → false；
// 浮点 RSI ∈ [min,max] 含两端边界（平价行情 RSI=50）。

// TestP4RSI区间快照 覆盖零值参数、上下边界恰等、非默认值、min>max 退化、
// 预热差一根与空行情。
func TestP4RSI区间快照(t *testing.T) {
	// 零值参数：15 根平价 → RSI=50，恰在上边界 [30,50] 内应触发。
	if !(RSI区间{}).Buy("sh600000", p4Flat(15, 10000, 100, 1e8)) {
		t.Fatal("零值参数：平价 RSI=50 恰等上边界 50 应触发")
	}
	// 末根大跌 → RSI=0，低于下边界不应触发；末根大涨 → RSI=100 高于上边界不应触发。
	fall := append(p4Flat(14, 10000, 100, 1e8), p4Bar(9000, 10000, 9000, 9000, 100, 1e8))
	if (RSI区间{}).Buy("sh600000", fall) {
		t.Fatal("RSI=0 低于下边界 30 不应触发")
	}
	rise := append(p4Flat(14, 10000, 100, 1e8), p4Bar(11000, 10000, 11000, 11000, 100, 1e8))
	if (RSI区间{}).Buy("sh600000", rise) {
		t.Fatal("RSI=100 高于上边界 50 不应触发")
	}
	// 上下边界恰等：Min=Max=50 平价 RSI=50 应触发；Min=51 时 RSI=50 低于下界不触发。
	if !(RSI区间{Min: 50, Max: 50}).Buy("sh600000", p4Flat(15, 10000, 100, 1e8)) {
		t.Fatal("Min=Max=50 且 RSI=50 应触发")
	}
	if (RSI区间{Min: 51, Max: 99}).Buy("sh600000", p4Flat(15, 10000, 100, 1e8)) {
		t.Fatal("RSI=50 低于下界 51 不应触发")
	}
	// 非默认参数 period=1（RSI 仅取 0/50/100）：RSI=0 落在 [-10,50] 内应触发；
	// RSI=100 落在 [60,200] 内应触发。
	if !(RSI区间{Period: 1, Min: -10, Max: 50}).Buy("sh600000",
		p4Ks(p4Bar(10000, 10000, 10000, 10000, 100, 1e8), p4Bar(9000, 10000, 9000, 9000, 100, 1e8))) {
		t.Fatal("period=1：RSI=0 在 [-10,50] 内应触发")
	}
	if !(RSI区间{Period: 1, Min: 60, Max: 200}).Buy("sh600000",
		p4Ks(p4Bar(10000, 10000, 10000, 10000, 100, 1e8), p4Bar(11000, 10000, 11000, 11000, 100, 1e8))) {
		t.Fatal("period=1：RSI=100 在 [60,200] 内应触发")
	}
	// min>max 退化：区间不可满足恒 false。
	if (RSI区间{Min: 80, Max: 20}).Buy("sh600000", p4Flat(15, 10000, 100, 1e8)) {
		t.Fatal("min>max 时任何 RSI 不应触发")
	}
	// 预热差一根：period=14 需 15 根；period=1 需 2 根。
	if (RSI区间{}).Buy("sh600000", p4Flat(14, 10000, 100, 1e8)) {
		t.Fatal("预热差一根（14 根算 14 日 RSI）不应触发")
	}
	if (RSI区间{Period: 1, Min: -10, Max: 50}).Buy("sh600000", p4Flat(1, 10000, 100, 1e8)) {
		t.Fatal("预热差一根（1 根算 1 日 RSI）不应触发")
	}
	// 空行情。
	if (RSI区间{}).Buy("sh600000", nil) {
		t.Fatal("空行情不应触发")
	}
}

// ---- A成交额大于（buy_turnover_min）----
// 旧实现：min 0→50000000 元；len==0 → false；当日成交额(元) >= min。
// Kline.Amount 为厘：5000 万元 = 5e10 厘。

// TestP4成交额大于快照 覆盖零值参数、阈值相等、非默认值、零额行情、
// 负阈值与空行情。
func TestP4成交额大于快照(t *testing.T) {
	// 零值参数（min 0→5e7 元）：成交额恰 5000 万应触发（>=），4999 万不触发。
	if !(A成交额大于(0)).Buy("sh600000", p4Flat(1, 10000, 100, 5e10)) {
		t.Fatal("零值参数：成交额恰 5000 万应触发")
	}
	if (A成交额大于(0)).Buy("sh600000", p4Flat(1, 10000, 100, 49990000*1000)) {
		t.Fatal("成交额 4999 万低于默认下限不应触发")
	}
	// 阈值相等与非默认值：min=3000 万，成交额恰等触发，差一厘不触发。
	if !(A成交额大于(30000000)).Buy("sh600000", p4Flat(1, 10000, 100, 3e10)) {
		t.Fatal("成交额恰 3000 万（>= 含边界）应触发")
	}
	if (A成交额大于(30000000)).Buy("sh600000", p4Flat(1, 10000, 100, 3e10-1)) {
		t.Fatal("成交额差一厘不应触发")
	}
	// 多根前缀只看当日成交额。
	if !(A成交额大于(30000000)).Buy("sh600000",
		p4Ks(p4Bar(10000, 10000, 10000, 10000, 100, 1e9), p4Bar(10000, 10000, 10000, 10000, 100, 3e10))) {
		t.Fatal("当日成交额恰 3000 万应触发（历史根不影响）")
	}
	// 零额行情：0 < 默认下限不触发。
	if (A成交额大于(0)).Buy("sh600000", p4Flat(1, 10000, 0, 0)) {
		t.Fatal("零额行情不应触发")
	}
	// 负阈值原样生效（0 已被默认替换，无法表达"不限"）。
	if !(A成交额大于(-1)).Buy("sh600000", p4Flat(1, 10000, 0, 0)) {
		t.Fatal("负阈值下零额行情应触发")
	}
	// 空行情。
	if (A成交额大于(0)).Buy("sh600000", nil) {
		t.Fatal("空行情不应触发")
	}
}

// ---- ATR波动率范围（buy_atr_range）----
// 旧实现：Period 0→14、MinPct 0→0.5、MaxPct 0→5.0；len<period+1 → false；
// 今收 <=0 → false；pct=ATR/今收×100 ∈ [min,max] 含两端；max<min 恒 false。
// ATR 为前 period 个真实波幅（不含更早）的均值，预热需 period+1 根。

// TestP4ATR波动率范围快照 覆盖零值参数、上下边界恰等、非默认值、
// max<min 退化、今收为 0、预热差一根与空行情。
func TestP4ATR波动率范围快照(t *testing.T) {
	flat14 := p4Flat(14, 10000, 100, 1e8)
	// 零值参数：前 14 根平价（TR=0），末根振幅 1000 厘 → ATR=1000/14 厘，
	// pct≈0.714% 落在默认 [0.5,5] 内应触发。
	wild := append(flat14, p4Bar(10000, 10500, 9500, 10000, 100, 1e8))
	if !(ATR波动率范围{}).Buy("sh600000", wild) {
		t.Fatal("零值参数：pct≈0.714% 在 [0.5,5] 内应触发")
	}
	// 波动过小：TR=100 厘 → pct≈0.071% 低于下界不应触发。
	calm := append(flat14, p4Bar(10000, 10050, 9950, 10000, 100, 1e8))
	if (ATR波动率范围{}).Buy("sh600000", calm) {
		t.Fatal("pct≈0.071% 低于下界 0.5 不应触发")
	}
	// 波动过大：TR=10000 厘 → pct≈7.14% 高于上界不应触发。
	crazy := append(flat14, p4Bar(10000, 15000, 5000, 10000, 100, 1e8))
	if (ATR波动率范围{}).Buy("sh600000", crazy) {
		t.Fatal("pct≈7.14% 高于上界 5 不应触发")
	}
	// 上下边界恰等（period=1、MinPct=MaxPct=10；价格取 0.5 元倍数保证元域
	// 二进制精确，避免厘转元后的减法误差，如 10.2-9.8=0.3999999999999986）：
	// TR=10.5-9.5=1.0 元精确、今收 10 元 → pct 恰为 10，含边界应触发；
	// TR 差 1 厘（0.999/1.001 元）落在界外不应触发。
	two := append(p4Flat(1, 10000, 100, 1e8), p4Bar(10000, 10500, 9500, 10000, 100, 1e8))
	if !(ATR波动率范围{Period: 1, MinPct: 10, MaxPct: 10}).Buy("sh600000", two) {
		t.Fatal("pct 恰为 10（[10,10] 含两端）应触发")
	}
	less := append(p4Flat(1, 10000, 100, 1e8), p4Bar(10000, 10500, 9501, 10000, 100, 1e8))
	if (ATR波动率范围{Period: 1, MinPct: 10, MaxPct: 10}).Buy("sh600000", less) {
		t.Fatal("TR=0.999 元 pct<10 不应触发")
	}
	more := append(p4Flat(1, 10000, 100, 1e8), p4Bar(10000, 10501, 9500, 10000, 100, 1e8))
	if (ATR波动率范围{Period: 1, MinPct: 10, MaxPct: 10}).Buy("sh600000", more) {
		t.Fatal("TR=1.001 元 pct>10 不应触发")
	}
	// 上界默认值恰等：TR=7000 厘 → ATR=0.5 元 → pct 恰为 5 应触发；超出则不应触发。
	maxEq := append(flat14, p4Bar(10000, 17000, 10000, 10000, 100, 1e8))
	if !(ATR波动率范围{}).Buy("sh600000", maxEq) {
		t.Fatal("pct 恰为 5（默认上界含边界）应触发")
	}
	maxOver := append(flat14, p4Bar(10000, 17001, 10000, 10000, 100, 1e8))
	if (ATR波动率范围{}).Buy("sh600000", maxOver) {
		t.Fatal("pct 略高于 5 不应触发")
	}
	// max<min 退化：区间不可满足恒 false。
	if (ATR波动率范围{MinPct: 5, MaxPct: 1}).Buy("sh600000", wild) {
		t.Fatal("max<min 时不应触发")
	}
	// 今收为 0：旧实现 close<=0 守卫返回 false。
	zeroClose := append(flat14, p4Bar(0, 0, 0, 0, 100, 1e8))
	if (ATR波动率范围{}).Buy("sh600000", zeroClose) {
		t.Fatal("今收为 0 不应触发")
	}
	// 预热差一根：period=14 需 15 根。
	if (ATR波动率范围{}).Buy("sh600000", p4Flat(14, 10000, 100, 1e8)) {
		t.Fatal("预热差一根（14 根算 14 日 ATR）不应触发")
	}
	// 非默认 period=1：TR≈0.4 元 → pct≈4，在 [3,5] 内应触发；[4.5,5] 外不应触发。
	mild := append(p4Flat(1, 10000, 100, 1e8), p4Bar(10000, 10200, 9800, 10000, 100, 1e8))
	if !(ATR波动率范围{Period: 1, MinPct: 3, MaxPct: 5}).Buy("sh600000", mild) {
		t.Fatal("period=1：pct≈4 在 [3,5] 内应触发")
	}
	if (ATR波动率范围{Period: 1, MinPct: 4.5, MaxPct: 5}).Buy("sh600000", mild) {
		t.Fatal("pct≈4 低于下界 4.5 不应触发")
	}
	// 空行情。
	if (ATR波动率范围{}).Buy("sh600000", nil) {
		t.Fatal("空行情不应触发")
	}
}

// ---- A突破N日高点（buy_break_high）----
// 旧实现：Period 0→20；len<period+1 → false；今收(浮点元) > 前 period 日
// 最高价（不含今日，HHV 取 High 字段）严格大于。

// TestP4突破N日高点快照 覆盖零值参数、恰等于N天最高、不含今日口径、
// High 字段口径、预热差一根、非默认周期与空行情。
func TestP4突破N日高点快照(t *testing.T) {
	// 零值参数：20 根平价 + 今收 10100 → 突破 10000 应触发。
	if !(A突破N日高点{}).Buy("sh600000",
		append(p4Flat(20, 10000, 100, 1e8), p4Bar(10100, 10100, 10100, 10100, 100, 1e8))) {
		t.Fatal("零值参数：今收 10100 > 前 20 日最高 10000 应触发")
	}
	// 恰等于N天最高：今收 == 10000（严格大于）不应触发。
	if (A突破N日高点{}).Buy("sh600000", p4Flat(21, 10000, 100, 1e8)) {
		t.Fatal("今收恰等于前 20 日最高（严格大于）不应触发")
	}
	// 不含今日口径：今日自身 High=12000 但收盘 10100，前 20 日最高仍 10000 →
	// 应触发（若错误地含今日，前 high 为 12000 则不触发）。
	if !(A突破N日高点{}).Buy("sh600000",
		append(p4Flat(20, 10000, 100, 1e8), p4Bar(10100, 12000, 10000, 10100, 100, 1e8))) {
		t.Fatal("窗口不含今日：今收 10100 > 前 20 日最高 10000 应触发")
	}
	// High 字段口径：昨日最高 10800（收盘 10000），今收 10500 不触发、10900 触发。
	yesterdaySpike := append(p4Flat(19, 10000, 100, 1e8),
		p4Bar(10000, 10800, 10000, 10000, 100, 1e8))
	if (A突破N日高点{Period: 20}).Buy("sh600000",
		append(yesterdaySpike, p4Bar(10500, 10500, 10500, 10500, 100, 1e8))) {
		t.Fatal("今收 10500 <= 昨日最高 10800 不应触发")
	}
	if !(A突破N日高点{Period: 20}).Buy("sh600000",
		append(yesterdaySpike, p4Bar(10900, 10900, 10900, 10900, 100, 1e8))) {
		t.Fatal("今收 10900 > 昨日最高 10800 应触发")
	}
	// 预热差一根：period=20 需 21 根；period=5 需 6 根。
	if (A突破N日高点{}).Buy("sh600000", p4Flat(20, 10000, 100, 1e8)) {
		t.Fatal("预热差一根（20 根算 20 日窗口）不应触发")
	}
	if (A突破N日高点{Period: 5}).Buy("sh600000", p4Flat(5, 10000, 100, 1e8)) {
		t.Fatal("预热差一根（5 根算 5 日窗口）不应触发")
	}
	// 非默认 period=5。
	if !(A突破N日高点{Period: 5}).Buy("sh600000",
		append(p4Flat(5, 10000, 100, 1e8), p4Bar(10100, 10100, 10100, 10100, 100, 1e8))) {
		t.Fatal("period=5：今收 10100 > 前 5 日最高 10000 应触发")
	}
	if (A突破N日高点{Period: 5}).Buy("sh600000",
		append(p4Flat(5, 10000, 100, 1e8), p4Bar(9900, 9900, 9900, 9900, 100, 1e8))) {
		t.Fatal("period=5：今收 9900 未突破不应触发")
	}
	// 空行情。
	if (A突破N日高点{}).Buy("sh600000", nil) {
		t.Fatal("空行情不应触发")
	}
}

// ---- A均线多头排列（buy_ma_bull）----
// 旧实现：nil/空 → [5,10,20]；len<max(periods) → false；相邻均线严格递减
//（extend 整数厘均线）。单周期退化为仅预热门槛（无相邻比较）。

// TestP4均线多头排列快照 覆盖零值参数、多头/空头排列、相邻恰等、中断链、
// 非默认周期、预热差一根、单周期退化与空行情。
func TestP4均线多头排列快照(t *testing.T) {
	// 零值参数：16 根平价 10 元 + 10000,10200,10400,10600 →
	// MA5=10240 > MA10=10120 > MA20=10060 应触发。
	bull := append(p4Flat(16, 10000, 100, 1e8),
		p4Bar(10000, 10000, 10000, 10000, 100, 1e8),
		p4Bar(10200, 10200, 10200, 10200, 100, 1e8),
		p4Bar(10400, 10400, 10400, 10400, 100, 1e8),
		p4Bar(10600, 10600, 10600, 10600, 100, 1e8))
	if !(A均线多头排列{}).Buy("sh600000", bull) {
		t.Fatal("零值参数：MA5>MA10>MA20 多头排列应触发")
	}
	// 空头排列：末四根回落 → MA5=9760 <= MA10=9880 不应触发。
	bear := append(p4Flat(16, 10000, 100, 1e8),
		p4Bar(10000, 10000, 10000, 10000, 100, 1e8),
		p4Bar(9800, 9800, 9800, 9800, 100, 1e8),
		p4Bar(9600, 9600, 9600, 9600, 100, 1e8),
		p4Bar(9400, 9400, 9400, 9400, 100, 1e8))
	if (A均线多头排列{}).Buy("sh600000", bear) {
		t.Fatal("空头排列不应触发")
	}
	// 相邻两日恰相等：全平价 MA 全相等，严格递减不成立不应触发。
	if (A均线多头排列{}).Buy("sh600000", p4Flat(20, 10000, 100, 1e8)) {
		t.Fatal("相邻均线恰相等（严格大于）不应触发")
	}
	// 中断链：MA5>MA10 成立但 MA10<=MA20 不应触发。
	// 前 5 根 12 元、13 根 10 元、末三根 10000/10100/10200：
	// MA5=10060 > MA10=10030，但 MA10=10030 <= MA20=10515。
	broken := append(p4Flat(5, 12000, 100, 1e8), p4Flat(12, 10000, 100, 1e8)...)
	broken = append(broken,
		p4Bar(10000, 10000, 10000, 10000, 100, 1e8),
		p4Bar(10100, 10100, 10100, 10100, 100, 1e8),
		p4Bar(10200, 10200, 10200, 10200, 100, 1e8))
	if (A均线多头排列{}).Buy("sh600000", broken) {
		t.Fatal("MA10<=MA20 中断链不应触发")
	}
	// 非默认周期 [3,6]：MA3=10200 > MA6=10100 应触发；回落则不应触发。
	up6 := append(p4Flat(5, 10000, 100, 1e8), p4Bar(10600, 10600, 10600, 10600, 100, 1e8))
	if !(A均线多头排列{3, 6}).Buy("sh600000", up6) {
		t.Fatal("[3,6]：MA3=10200 > MA6=10100 应触发")
	}
	down6 := append(p4Flat(5, 10000, 100, 1e8), p4Bar(9400, 9400, 9400, 9400, 100, 1e8))
	if (A均线多头排列{3, 6}).Buy("sh600000", down6) {
		t.Fatal("[3,6]：MA3=9800 < MA6=9900 不应触发")
	}
	// 预热差一根：默认需 20 根。
	if (A均线多头排列{}).Buy("sh600000", bull[:19]) {
		t.Fatal("预热差一根（19 根算 20 日均线）不应触发")
	}
	// 单周期 [5] 退化：窗口足即恒真（无相邻比较），窗口不足为 false。
	if !(A均线多头排列{5}).Buy("sh600000", p4Flat(5, 10000, 100, 1e8)) {
		t.Fatal("单周期 [5] 窗口足应触发（旧实现退化为仅预热检查）")
	}
	if (A均线多头排列{5}).Buy("sh600000", p4Flat(4, 10000, 100, 1e8)) {
		t.Fatal("单周期 [5] 窗口不足不应触发")
	}
	// 空行情。
	if (A均线多头排列{}).Buy("sh600000", nil) {
		t.Fatal("空行情不应触发")
	}
}

// ---- A实体阳线（buy_solid_bull）----
// 旧实现：MinBodyRatio 0→0.5；len==0 → false；收阳（今收>今开，浮点）且
// 实体振幅占比 |收-开|/(高-低) >= minRatio（振幅 <=0 → NaN → 不触发）。

// TestP4实体阳线快照 覆盖零值参数、非默认值、占比恰等、收阳判断、
// 十字星/阴线/平价行情与空行情。
func TestP4实体阳线快照(t *testing.T) {
	// 零值参数：实体 500 / 振幅 700 ≈ 0.714 >= 0.5 应触发。
	bull := p4Ks(p4Bar(10000, 10600, 9900, 10500, 100, 1e8))
	if !(A实体阳线{}).Buy("sh600000", bull) {
		t.Fatal("零值参数：实体占比 0.714 >= 0.5 应触发")
	}
	// 非默认 0.8：0.714 < 0.8 不应触发。
	if (A实体阳线{MinBodyRatio: 0.8}).Buy("sh600000", bull) {
		t.Fatal("实体占比 0.714 < 0.8 不应触发")
	}
	// 占比恰等 0.5（350/700）：含边界应触发；略高阈值不应触发。
	half := p4Ks(p4Bar(10000, 10600, 9900, 10350, 100, 1e8))
	if !(A实体阳线{MinBodyRatio: 0.5}).Buy("sh600000", half) {
		t.Fatal("实体占比恰为 0.5（>= 含边界）应触发")
	}
	if (A实体阳线{MinBodyRatio: 0.500001}).Buy("sh600000", half) {
		t.Fatal("实体占比 0.5 低于阈值 0.500001 不应触发")
	}
	// 占比恰为 1（光头光脚）：阈值 1 含边界应触发，1.1 不应触发。
	full := p4Ks(p4Bar(10000, 10500, 10000, 10500, 100, 1e8))
	if !(A实体阳线{MinBodyRatio: 1}).Buy("sh600000", full) {
		t.Fatal("实体占比恰为 1 应触发")
	}
	if (A实体阳线{MinBodyRatio: 1.1}).Buy("sh600000", full) {
		t.Fatal("实体占比 1 < 1.1 不应触发")
	}
	// 平收十字星（今收==今开）：收阳不成立不应触发。
	doji := p4Ks(p4Bar(10200, 10500, 9900, 10200, 100, 1e8))
	if (A实体阳线{}).Buy("sh600000", doji) {
		t.Fatal("平收十字星（收==开）不应触发")
	}
	// 阴线不应触发。
	bearLine := p4Ks(p4Bar(10500, 10600, 9900, 10000, 100, 1e8))
	if (A实体阳线{}).Buy("sh600000", bearLine) {
		t.Fatal("阴线不应触发")
	}
	// 平价行情（开=高=低=收）：收阳不成立且占比 NaN 不应触发。
	if (A实体阳线{}).Buy("sh600000", p4Flat(1, 10000, 100, 1e8)) {
		t.Fatal("平价行情不应触发")
	}
	// 负阈值原样生效（0 已被 0.5 替换）：任意阳线占比 >= -1 应触发。
	if !(A实体阳线{MinBodyRatio: -1}).Buy("sh600000", bull) {
		t.Fatal("负阈值下阳线应触发")
	}
	// 空行情。
	if (A实体阳线{}).Buy("sh600000", nil) {
		t.Fatal("空行情不应触发")
	}
}
