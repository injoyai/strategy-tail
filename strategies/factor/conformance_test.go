package factor

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// ---- 新增因子一致性测试（规范 §6.2）----
// 对逐日历史前缀求值，验证：
//  1. Build 与 BuildContext（Contextual 适配）数值一致（含 NaN 判等）；
//  2. 同一实例重复求值结果一致（无跨调用可变状态）；
//  3. 求值不修改输入K线（含内嵌 protocol.Kline 与 Turnover）。
//
// 数值正确性由各因子自身的可手算测试覆盖，此处不做公式断言。

// conformanceKs 构造 n 根确定性合成K线（不依赖随机数与当前时间）：
//   - close = 10 + 0.05i + 2sin(i/7)：>0 且有涨有跌；
//   - open = 前收（首根取 10）；
//   - high = max(o,c) + 0.1+0.05|sin(i/3)|，low = min(o,c) − (0.1+0.05|sin(i/5)|)，
//     保证 high≥max(o,c)≥min(o,c)≥low>0；
//   - volume = 10000+1000(i%13)（恒>0），Turnover = 2+0.1(i%7)（恒>0，百分数刻度）。
func conformanceKs(base time.Time, n int) extend.Klines {
	ks := make(extend.Klines, 0, n)
	for i := 0; i < n; i++ {
		c := 10 + 0.05*float64(i) + 2*math.Sin(float64(i)/7)
		o := 10.0
		if i > 0 {
			o = 10 + 0.05*float64(i-1) + 2*math.Sin(float64(i-1)/7)
		}
		h := math.Max(o, c) + 0.1 + 0.05*math.Abs(math.Sin(float64(i)/3))
		l := math.Min(o, c) - (0.1 + 0.05*math.Abs(math.Sin(float64(i)/5)))
		k := mk(base, i, o, h, l, c, int64(10000+1000*(i%13)))
		k.Turnover = 2 + 0.1*float64(i%7)
		ks = append(ks, k)
	}
	return ks
}

// cloneKs 深拷贝K线切片（含内嵌 protocol.Kline），用于只读快照比对。
func cloneKs(ks extend.Klines) extend.Klines {
	out := make(extend.Klines, len(ks))
	for i, k := range ks {
		c := *k
		inner := *k.Kline
		c.Kline = &inner
		out[i] = &c
	}
	return out
}

// sameNaN 判等两个浮点值：NaN 与 NaN 视为相等。
func sameNaN(a, b float64) bool {
	return a == b || (math.IsNaN(a) && math.IsNaN(b))
}

// TestNewFactorsConformance 新增价量因子的一致性验证。
func TestNewFactorsConformance(t *testing.T) {
	// days=0 走注册默认值（20/60/1），同时覆盖工厂默认语义。
	kinds := []string{
		"downside_vol", "return_skew", "parkinson_vol",
		"turnover_mean", "turnover_pct", "volume_cv",
		"volume_up_ratio", "volume_down_ratio",
		"range_ratio", "bar_center",
	}
	const n = 120
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	code := "sh600000"

	for _, kind := range kinds {
		f := Build(kind, 0)
		if f == nil {
			t.Fatalf("%s: Build 返回 nil", kind)
		}
		ctx := BuildContext(kind, 0)
		if ctx == nil {
			t.Fatalf("%s: BuildContext 返回 nil", kind)
		}
		if f.Name() != ctx.Name() {
			t.Fatalf("%s: 名称不一致 Build=%q BuildContext=%q", kind, f.Name(), ctx.Name())
		}

		ks := conformanceKs(base, n)
		snapshot := cloneKs(ks)

		for i := 1; i <= n; i++ {
			prefix := ks[:i]
			v1 := f.Value(code, prefix)
			// 重复求值一致
			if v2 := f.Value(code, prefix); !sameNaN(v1, v2) {
				t.Fatalf("%s 前缀 %d 根: 重复求值不一致 %v vs %v", kind, i, v1, v2)
			}
			// Build 与 BuildContext 数值一致
			vc := ctx.ValueAt(core.FactorContext{
				Code:   code,
				AsOf:   prefix[len(prefix)-1].Time,
				Klines: prefix,
			})
			if !sameNaN(v1, vc) {
				t.Fatalf("%s 前缀 %d 根: Build=%v BuildContext=%v", kind, i, v1, vc)
			}
			// 足够历史长度下应输出确定数值（缺失值语义由单测覆盖）
			if i == n && math.IsNaN(v1) {
				t.Fatalf("%s 完整 %d 根输入不应返回 NaN", kind, n)
			}
		}

		// 输入只读
		if !reflect.DeepEqual(ks, snapshot) {
			t.Fatalf("%s: 求值修改了输入K线", kind)
		}
	}
}
