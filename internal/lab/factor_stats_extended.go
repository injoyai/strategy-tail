package lab

import (
	"math"
	"sort"
)

// factor_stats_extended.go：v4 扩展 IC 统计（Task 3）。全部纯函数无 IO，
// 无有效结果一律 JSON null，不用 0 冒充真实为零。

// ExtendedICStats 一组逐日 IC 的扩展汇总。Mean/Std 等为有限日 IC 的统计；
// 退化输入（无有效样本、Std=0、HAC 长期方差非正等）对应字段为 null。
// DirectionConsistentRate 是设计 8.2“预期负向因子在展示层给出方向一致率”
// 的后端计算字段（前端不做二次推断）：positive 时与 PositiveRate 相同，
// negative 时为 IC<0 比例，two_sided 或未知方向为 null。
type ExtendedICStats struct {
	Pairs                   int      `json:"pairs"`
	Mean                    *float64 `json:"mean"`
	Std                     *float64 `json:"std"`
	ICIR                    *float64 `json:"icir"`
	AnnualizedICIR          *float64 `json:"annualizedIcir"`
	PositiveRate            *float64 `json:"positiveRate"`
	DirectionConsistentRate *float64 `json:"directionConsistentRate"`
	NaiveTStat              *float64 `json:"naiveTStat"`
	HACTStat                *float64 `json:"hacTStat"`
	HACLag                  int      `json:"hacLag"`
	PValue                  *float64 `json:"pValue"`
}

// annualizationFactor 年化 ICIR 的日频固定系数 sqrt(252)。
const annualizationFactor = 252

// hacMeanTStat 按时间排序序列均值的 Newey-West HAC t 统计量：
//
//	LRV = γ0 + 2·Σ[l=1..L] (1 - l/(L+1))·γl，γl = Σ d_t·d_{t-l} / n（1/n 分母）
//	SE(mean) = sqrt(LRV / n)，t = mean / SE
//
// lag=0 时 LRV=γ0，SE 退化为朴素标准误（总体 std/√n）。l≥n 的自协方差无
// 重叠项恒为 0，等价于把 lag 截断到 n-1。样本不足（<2）、含 NaN/Inf、
// lag<0、严格常数序列（含不可精确表示值，LRV 数学上恒为 0）或 LRV<=0
// 等退化情形返回 (nil, nil)，不回退成朴素 t 值。
func hacMeanTStat(xs []float64, lag int) (t, se *float64) {
	n := len(xs)
	if n < 2 || lag < 0 {
		return nil, nil
	}
	sum := 0.0
	for _, v := range xs {
		if !finite(v) {
			return nil, nil
		}
		sum += v
	}
	if allEqual(xs) {
		return nil, nil
	}
	mean := sum / float64(n)
	d := make([]float64, n)
	ss := 0.0
	for i, v := range xs {
		d[i] = v - mean
		ss += d[i] * d[i]
	}
	gamma := func(l int) float64 { // γl = Σ d_t·d_{t-l} / n；l≥n 无重叠项
		g := 0.0
		for i := l; i < n; i++ {
			g += d[i] * d[i-l]
		}
		return g / float64(n)
	}
	lrv := ss / float64(n) // γ0
	for l := 1; l <= lag && l < n; l++ {
		lrv += 2 * (1 - float64(l)/float64(lag+1)) * gamma(l)
	}
	if lrv <= 0 {
		return nil, nil
	}
	stdErr := math.Sqrt(lrv / float64(n))
	v := mean / stdErr
	return &v, &stdErr
}

// finite 值为有限数（非 NaN 且非 ±Inf）。
func finite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

// allEqual 严格常数序列检测。均值舍入会使不可精确表示的常数（如 0.2×3）
// 产生 1e-17 级伪偏差，进而算出天文数字级伪 t 值，故按值相等精确判定。
func allEqual(xs []float64) bool {
	for _, v := range xs {
		if v != xs[0] {
			return false
		}
	}
	return true
}

// extendedICStats 汇总逐日 IC 为 ExtendedICStats：NaN/Inf 先剔除（保持时序
// 顺序，滞后按有效日序数计），再计算各统计。lag 为实际使用的 HAC 滞后
// （编排层由 HACLagMode 解析，horizon_minus_one 时 L=max(h-1,0)），原样
// 记录到 HACLag；lag<0 视为无效配置：HAC t 与 p 值为 null，HACLag 记 0。
// PValue 为 HAC t 的双侧正态近似 p = erfc(|t|/√2)——明确采用正态近似而非
// t 分布精确值（避免引入新依赖），小样本时偏乐观，仅作探索参考。
func extendedICStats(ics []float64, expectedDirection string, lag int) ExtendedICStats {
	valid := make([]float64, 0, len(ics))
	for _, v := range ics {
		if finite(v) {
			valid = append(valid, v)
		}
	}
	s := ExtendedICStats{Pairs: len(valid), HACLag: lag}
	if s.HACLag < 0 {
		s.HACLag = 0
	}
	n := len(valid)
	if n == 0 {
		return s
	}
	sum := 0.0
	positive := 0
	for _, v := range valid {
		sum += v
		if v > 0 {
			positive++
		}
	}
	mean := sum / float64(n)
	s.Mean = &mean
	posRate := float64(positive) / float64(n)
	s.PositiveRate = &posRate
	switch expectedDirection {
	case "positive":
		s.DirectionConsistentRate = &posRate
	case "negative":
		negRate := float64(n-positive) / float64(n)
		s.DirectionConsistentRate = &negRate
	}
	ss := 0.0
	for _, v := range valid {
		ss += (v - mean) * (v - mean)
	}
	if n >= 2 { // 单样本无离散度定义，Std 为 null
		if allEqual(valid) { // 常数序列：Std=0 是真实结果，除以它的推断统计无定义
			zero := 0.0
			s.Std = &zero
			return s
		}
		std := math.Sqrt(ss / float64(n)) // 总体标准差，与 v3 ICStats 口径一致
		s.Std = &std
		if std > 0 {
			icir := mean / std
			s.ICIR = &icir
			ann := math.Sqrt(annualizationFactor) * icir
			s.AnnualizedICIR = &ann
			naive := mean / (std / math.Sqrt(float64(n)))
			s.NaiveTStat = &naive
		}
	}
	// lag<0 为无效配置：HACLag 记 0 且不计算 HAC（hacMeanTStat 对 lag<0
	// 返回 nil，这里传原始 lag 而非 clamp 后的 HACLag）
	hacT, _ := hacMeanTStat(valid, lag)
	if hacT != nil {
		s.HACTStat = hacT
		p := math.Erfc(math.Abs(*hacT) / math.Sqrt(2))
		s.PValue = &p
	}
	return s
}

// HorizonDecayPoint IC 衰减曲线单点：全部为该 Horizon 的原始统计，相邻
// 周期之间不做任何插值或平滑。Spread 为首末组收益差（最高组减最低组）。
type HorizonDecayPoint struct {
	Horizon int      `json:"horizon"`
	MeanIC  *float64 `json:"meanIc"`
	HACT    *float64 `json:"hacT"`
	Spread  *float64 `json:"spread"`
}

// buildDecayCurve 按 Horizon 升序组装衰减点；某个 Horizon 缺统计或缺
// spread 时对应字段为 null，不以 0 冒充。horizons 顺序无关，输出恒升序。
func buildDecayCurve(horizons []int, stats map[int]ExtendedICStats, spreads map[int]*float64) []HorizonDecayPoint {
	if len(horizons) == 0 {
		return nil
	}
	sorted := append([]int(nil), horizons...)
	sort.Ints(sorted)
	points := make([]HorizonDecayPoint, 0, len(sorted))
	for _, h := range sorted {
		p := HorizonDecayPoint{Horizon: h}
		if st, ok := stats[h]; ok {
			p.MeanIC, p.HACT = st.Mean, st.HACTStat
		}
		if spreads != nil {
			p.Spread = spreads[h]
		}
		points = append(points, p)
	}
	return points
}
