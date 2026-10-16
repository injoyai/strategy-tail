package factor

import (
	"fmt"
	"math"

	"github.com/injoyai/strategy-tail/lib/extend"
)

// N日波动 是近 N 日日收益率的总体标准差（衡量波动强度，值越大越剧烈）。
// 数据不足（len < Days+1）或窗口内出现零收盘价返回 NaN。
type N日波动 struct {
	Days int
}

func (f N日波动) Name() string {
	return fmt.Sprintf("N日波动(%d)", daysOr(f.Days, 20))
}

func (f N日波动) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 20)
	if len(dks) < n+1 {
		return math.NaN()
	}
	w := dks[len(dks)-n-1:]
	var sum float64
	rets := make([]float64, 0, n)
	for i := 1; i < len(w); i++ {
		prev := w[i-1].Close.Float64()
		if prev == 0 {
			return math.NaN()
		}
		r := w[i].Close.Float64()/prev - 1
		rets = append(rets, r)
		sum += r
	}
	mean := sum / float64(n)
	var ss float64
	for _, r := range rets {
		ss += (r - mean) * (r - mean)
	}
	return math.Sqrt(ss / float64(n))
}

// N日振幅 是近 N 日价格区间占比：(HHV(High,N) - LLV(Low,N)) / LLV(Low,N)。
// 数据不足（len < Days）或 LLV=0 返回 NaN。
type N日振幅 struct {
	Days int
}

func (f N日振幅) Name() string {
	return fmt.Sprintf("N日振幅(%d)", daysOr(f.Days, 20))
}

func (f N日振幅) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 20)
	if len(dks) < n {
		return math.NaN()
	}
	llv := dks.LLV(n).Float64()
	if llv == 0 {
		return math.NaN()
	}
	return (dks.HHV(n).Float64() - llv) / llv
}

// N日下行波动 是近 N 日日收益率的下行波动：sqrt( Σ min(r_i,0)^2 / N )。
// 输出为小数比例（0.015 表示亏损侧日均波动约 1.5%），只衡量亏损侧风险。
// 参数 Days 为统计天数，Days<=0 时取默认 20；窗口为最近 N+1 根K线形成的 N 个日收益。
// 最少历史 len >= Days+1，不足返回 NaN。
// 缺失/退化处理：窗口内出现零收盘价返回 NaN；全正收益时为 0（合法值）。
type N日下行波动 struct {
	Days int
}

func (f N日下行波动) Name() string {
	return fmt.Sprintf("N日下行波动(%d)", daysOr(f.Days, 20))
}

func (f N日下行波动) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 20)
	if n >= len(dks) {
		return math.NaN()
	}
	w := dks[len(dks)-n-1:]
	var ss float64
	for i := 1; i < len(w); i++ {
		prev := w[i-1].Close.Float64()
		if prev == 0 {
			return math.NaN()
		}
		r := w[i].Close.Float64()/prev - 1
		if r < 0 {
			ss += r * r
		}
	}
	return math.Sqrt(ss / float64(n))
}

// N日收益偏度 是近 N 日日收益率分布的偏度：m3 / m2^1.5，
// 其中 m2、m3 为相对均值的二、三阶中心矩（分母均为 N）。
// 输出无量纲：0 为对称，负为左偏（长左尾），正为右偏。
// 参数 Days 为统计天数，Days<=0 时取默认 20；窗口为最近 N+1 根K线形成的 N 个日收益。
// 最少历史 len >= Days+1 且 N >= 3，不足返回 NaN。
// 缺失/退化处理：窗口内出现零收盘价，或收益标准差 <= 1e-14·max(1,|均值|) 时返回 NaN。
type N日收益偏度 struct {
	Days int
}

func (f N日收益偏度) Name() string {
	return fmt.Sprintf("N日收益偏度(%d)", daysOr(f.Days, 20))
}

func (f N日收益偏度) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 20)
	if n < 3 || n >= len(dks) {
		return math.NaN()
	}
	w := dks[len(dks)-n-1:]
	var sum float64
	rets := make([]float64, 0, n)
	for i := 1; i < len(w); i++ {
		prev := w[i-1].Close.Float64()
		if prev == 0 {
			return math.NaN()
		}
		r := w[i].Close.Float64()/prev - 1
		rets = append(rets, r)
		sum += r
	}
	mean := sum / float64(n)
	var m2, m3 float64
	for _, r := range rets {
		d := r - mean
		m2 += d * d
		m3 += d * d * d
	}
	m2 /= float64(n)
	m3 /= float64(n)
	// 恒定收益的除法舍入噪声不能被三阶标准化放大成偏度。
	if m2 <= 1e-28*math.Max(1, mean*mean) {
		return math.NaN()
	}
	return m3 / math.Pow(m2, 1.5)
}

// N日Parkinson波动 是近 N 日（含今日）基于高低价的 Parkinson 波动率：
// sqrt( mean( ln(High/Low)^2 ) / (4·ln2) )。
// 输出为小数比例（0.03 表示由日内高低区间估计的日波动约 3%）。
// 参数 Days 为统计天数，Days<=0 时取默认 20；窗口为最近 N 根K线（含今日）。
// 最少历史 len >= Days，不足返回 NaN。
// 缺失/退化处理：一字线（High=Low）的 ln 项按 0 计入；Low<=0 或 High<Low 返回 NaN。
type N日Parkinson波动 struct {
	Days int
}

func (f N日Parkinson波动) Name() string {
	return fmt.Sprintf("N日Parkinson波动(%d)", daysOr(f.Days, 20))
}

func (f N日Parkinson波动) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 20)
	if len(dks) < n {
		return math.NaN()
	}
	var sum float64
	for _, k := range dks[len(dks)-n:] {
		h, l := k.High.Float64(), k.Low.Float64()
		if l <= 0 || h < l {
			return math.NaN()
		}
		if h == l {
			continue
		}
		d := math.Log(h / l)
		sum += d * d
	}
	return math.Sqrt(sum / float64(n) / (4 * math.Log(2)))
}
