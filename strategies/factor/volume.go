package factor

import (
	"fmt"
	"math"

	"github.com/injoyai/strategy-tail/lib/extend"
)

// 量比 是今日成交量相对前 N 日均量的倍数（不含今日）。
// 数据不足（len < Days+1）或前 N 日均量为 0 返回 NaN。
type 量比 struct {
	Days int
}

func (f 量比) Name() string {
	return fmt.Sprintf("量比(%d)", daysOr(f.Days, 5))
}

func (f 量比) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 5)
	if len(dks) < n+1 {
		return math.NaN()
	}
	w := dks[len(dks)-n-1 : len(dks)-1]
	var sum int64
	for _, k := range w {
		sum += k.Volume
	}
	avg := float64(sum) / float64(n)
	if avg == 0 {
		return math.NaN()
	}
	return float64(dks[len(dks)-1].Volume) / avg
}

// 量分位 是今量在近 N 日（含今日）中的分位：vol ≤ 今量 的天数占比。
// 数据不足（len < Days）返回 NaN。
type 量分位 struct {
	Days int
}

func (f 量分位) Name() string {
	return fmt.Sprintf("量分位(%d)", daysOr(f.Days, 60))
}

func (f 量分位) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 60)
	if len(dks) < n {
		return math.NaN()
	}
	today := dks[len(dks)-1].Volume
	var cnt int
	for _, k := range dks[len(dks)-n:] {
		if k.Volume <= today {
			cnt++
		}
	}
	return float64(cnt) / float64(n)
}

// 放量占比 是近 N 日（含今日）中成交量超过 1.5 倍日均量的天数占比。
// 数据不足（len < Days）返回 NaN。
type 放量占比 struct {
	Days int
}

func (f 放量占比) Name() string {
	return fmt.Sprintf("放量占比(%d)", daysOr(f.Days, 20))
}

func (f 放量占比) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 20)
	if len(dks) < n {
		return math.NaN()
	}
	w := dks[len(dks)-n:]
	var sum int64
	for _, k := range w {
		sum += k.Volume
	}
	threshold := 1.5 * float64(sum) / float64(n)
	var cnt int
	for _, k := range w {
		if float64(k.Volume) > threshold {
			cnt++
		}
	}
	return float64(cnt) / float64(n)
}

// turnoverRatio 返回单根K线换手率，单位为百分数（2.5 表示 2.5%）。
// Turnover>0 直接取值；有成交量但 Turnover 缺失（为 0）视为数据缺失返回 NaN；
// 无成交（量为 0）按换手率 0 处理。
func turnoverRatio(k *extend.Kline) float64 {
	if k.Turnover > 0 {
		return k.Turnover
	}
	if k.Volume <= 0 {
		return 0
	}
	return math.NaN()
}

// 平均换手率 是近 N 日（含今日）日均换手率：mean(换手率_i)。
// 输出单位为百分数（2.5 表示日均换手 2.5%），数值越大换手越活跃。
// 参数 Days 为统计天数，Days<=0 时取默认 20；窗口为最近 N 根K线（含今日）。
// 最少历史 len >= Days，不足返回 NaN。
// 缺失/退化处理：窗口内有成交但换手率缺失（Turnover 为 0）返回 NaN；无成交日按换手率 0 计入。
type 平均换手率 struct {
	Days int
}

func (f 平均换手率) Name() string {
	return fmt.Sprintf("平均换手率(%d)", daysOr(f.Days, 20))
}

func (f 平均换手率) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 20)
	if len(dks) < n {
		return math.NaN()
	}
	var sum float64
	for _, k := range dks[len(dks)-n:] {
		t := turnoverRatio(k)
		if math.IsNaN(t) {
			return math.NaN()
		}
		sum += t
	}
	return sum / float64(n)
}

// 换手率分位 是今换手率在近 N 日（含今日）中的分位：换手率 ≤ 今值 的有效天数占比。
// 输出为小数比例（0.9 表示今换手率不低于近 N 日中 90% 交易日的换手率）。
// 参数 Days 为统计窗口，Days<=0 时取默认 60；窗口为最近 N 根K线（含今日）。
// 最少历史 len >= Days，不足返回 NaN。
// 缺失/退化处理：今换手率缺失（有成交但 Turnover 为 0）返回 NaN；
// 历史中换手率缺失的交易日不计入分子与分母。
type 换手率分位 struct {
	Days int
}

func (f 换手率分位) Name() string {
	return fmt.Sprintf("换手率分位(%d)", daysOr(f.Days, 60))
}

func (f 换手率分位) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 60)
	if len(dks) < n {
		return math.NaN()
	}
	w := dks[len(dks)-n:]
	today := turnoverRatio(w[len(w)-1])
	if math.IsNaN(today) {
		return math.NaN()
	}
	var cnt, valid int
	for _, k := range w {
		t := turnoverRatio(k)
		if math.IsNaN(t) {
			continue
		}
		valid++
		if t <= today {
			cnt++
		}
	}
	return float64(cnt) / float64(valid)
}

// 量波动 是近 N 日（含今日）成交量的变异系数：总体标准差 / 均量。
// 输出为倍数（0.5 表示量标准差约为均量的一半），值越大量能越不稳定。
// 参数 Days 为统计天数，Days<=0 时取默认 20；窗口为最近 N 根K线（含今日）。
// 最少历史 len >= Days，不足返回 NaN。
// 缺失/退化处理：均量为 0（窗口全部零量）返回 NaN；零量日按合法值参与统计。
type 量波动 struct {
	Days int
}

func (f 量波动) Name() string {
	return fmt.Sprintf("量波动(%d)", daysOr(f.Days, 20))
}

func (f 量波动) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 20)
	if len(dks) < n {
		return math.NaN()
	}
	w := dks[len(dks)-n:]
	var sum int64
	for _, k := range w {
		sum += k.Volume
	}
	mean := float64(sum) / float64(n)
	if mean == 0 {
		return math.NaN()
	}
	var ss float64
	for _, k := range w {
		d := float64(k.Volume) - mean
		ss += d * d
	}
	return math.Sqrt(ss/float64(n)) / mean
}

// 阳线量比 是近 N 日（含今日）阳线（收盘>开盘）日均量相对全窗口日均量的倍数。
// 输出为倍数（1.2 表示阳线日均量为窗口均量的 1.2 倍），衡量上涨日的量能倾向。
// 参数 Days 为统计天数，Days<=0 时取默认 20；窗口为最近 N 根K线（含今日）。
// 最少历史 len >= Days，不足返回 NaN。
// 缺失/退化处理：平盘日（收盘=开盘）不计入分子；窗口内无阳线或均量为 0 返回 NaN。
type 阳线量比 struct {
	Days int
}

func (f 阳线量比) Name() string {
	return fmt.Sprintf("阳线量比(%d)", daysOr(f.Days, 20))
}

func (f 阳线量比) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 20)
	if len(dks) < n {
		return math.NaN()
	}
	return upDownVolumeRatio(dks[len(dks)-n:], func(c, o float64) bool { return c > o })
}

// 阴线量比 是近 N 日（含今日）阴线（收盘<开盘）日均量相对全窗口日均量的倍数。
// 输出为倍数（0.8 表示阴线日均量为窗口均量的 0.8 倍），衡量下跌日的量能倾向。
// 参数 Days 为统计天数，Days<=0 时取默认 20；窗口为最近 N 根K线（含今日）。
// 最少历史 len >= Days，不足返回 NaN。
// 缺失/退化处理：平盘日（收盘=开盘）不计入分子；窗口内无阴线或均量为 0 返回 NaN。
type 阴线量比 struct {
	Days int
}

func (f 阴线量比) Name() string {
	return fmt.Sprintf("阴线量比(%d)", daysOr(f.Days, 20))
}

func (f 阴线量比) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 20)
	if len(dks) < n {
		return math.NaN()
	}
	return upDownVolumeRatio(dks[len(dks)-n:], func(c, o float64) bool { return c < o })
}

// upDownVolumeRatio 计算窗口内满足 cmp(收盘,开盘) 的交易日日均量相对全窗口均量的倍数。
func upDownVolumeRatio(w extend.Klines, cmp func(c, o float64) bool) float64 {
	var sum, sel int64
	var selCnt int
	for _, k := range w {
		sum += k.Volume
		if cmp(k.Close.Float64(), k.Open.Float64()) {
			sel += k.Volume
			selCnt++
		}
	}
	if selCnt == 0 {
		return math.NaN()
	}
	avg := float64(sum) / float64(len(w))
	if avg == 0 {
		return math.NaN()
	}
	return float64(sel) / float64(selCnt) / avg
}
