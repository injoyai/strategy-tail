package lab

import (
	"math"
	"sort"
	"strings"
)

// 中性化纯函数：单个交易日截面上的可选风险暴露处理。流程与设计文档 9.1
// 固定一致：过滤无效值 → 可选 MAD 去极值 → 标准化 →（industry_size）对行业
// 哑变量和 log(市值) 做截面回归取残差。函数无 IO、无时钟：输入内部按代码
// 稳定排序，相同集合无论输入顺序如何输出逐位一致。

// ExposureObservation 中性化输入：一个交易日的截面观测。Industry 为空或
// Size 非正表示 PIT View 缺字段——按缺失丢弃并披露，绝不用零填充伪装。
type ExposureObservation struct {
	Code     string
	Value    float64 // 原始因子值，要求有限
	Industry string  // 行业分类（industry_size 模式必填）
	Size     float64 // 流通市值（industry_size 模式必填，须 > 0）
}

// 中性化结果状态：ok 输出可用；unavailable 表示无法产生可信输出（Null 语义：
// Values 保持为空，不得用零或原始值冒充）。
const (
	neutralizationStatusOK          = "ok"
	neutralizationStatusUnavailable = "unavailable"
)

// MAD 去极值参数与回归下限。minRegressionSamples 是截面回归的统计意义下限，
// 不是金融规律。
const (
	madWinsorThreshold   = 3.0
	madConsistencyScale  = 1.4826
	minRegressionSamples = 10
	olsSingularityRelTol = 1e-10
)

// NeutralizationResult 中性化输出。Values 的键为代码，值为该截面最终用于
// IC/分组诊断的因子值（none 模式为标准化值；industry_size 模式为残差）。
// Notes 披露数据质量事实（缺行业、缺市值、MAD 退化等），供报告呈现。
type NeutralizationResult struct {
	Status  string             `json:"status"`
	Reason  string             `json:"reason,omitempty"`
	Values  map[string]float64 `json:"values,omitempty"`
	Used    int                `json:"used"`
	Dropped int                `json:"dropped"`
	Notes   []string           `json:"notes,omitempty"`
}

// neutralizeCrossSection 单日截面中性化入口。spec 先经 ResearchProtocol.
// Validate 白名单校验；此处对非法模式保守返回 unavailable。
func neutralizeCrossSection(obs []ExposureObservation, spec NeutralizationSpec) NeutralizationResult {
	switch spec.Mode {
	case neutralizationNone:
		return neutralizeNone(obs, spec)
	case neutralizationIndustrySize:
		return neutralizeIndustrySize(obs, spec)
	default:
		return NeutralizationResult{Status: neutralizationStatusUnavailable, Reason: "中性化模式非法: " + spec.Mode}
	}
}

// neutralizeNone 无回归：过滤无效值 → 可选 MAD → 标准化。
func neutralizeNone(obs []ExposureObservation, spec NeutralizationSpec) NeutralizationResult {
	idx := sortedValidIndexes(obs)
	dropped := len(obs) - len(idx)
	if len(idx) == 0 {
		return NeutralizationResult{Status: neutralizationStatusUnavailable, Reason: "无有效因子值", Dropped: dropped}
	}
	notes := make([]string, 0, 2)
	vals := applyWinsorization(valuesAt(obs, idx), spec, &notes)
	out, reason := standardizeCrossSection(vals, spec.Standardization)
	if reason != "" {
		return NeutralizationResult{Status: neutralizationStatusUnavailable, Reason: reason, Dropped: dropped, Notes: notes}
	}
	return NeutralizationResult{Status: neutralizationStatusOK, Values: zipValues(obs, idx, out), Used: len(idx), Dropped: dropped, Notes: notes}
}

// neutralizeIndustrySize 过滤无效值与缺暴露观测 → MAD → 标准化 → 行业 dummy
// + log(市值) OLS 残差。
func neutralizeIndustrySize(obs []ExposureObservation, spec NeutralizationSpec) NeutralizationResult {
	// 过滤与缺失披露：行业/市值缺失的观测被丢弃，不以零填充。
	idx := make([]int, 0, len(obs))
	dropped := 0
	missingIndustry, missingSize := 0, 0
	for i, o := range obs {
		switch {
		case strings.TrimSpace(o.Code) == "" || !finite(o.Value):
			dropped++
		case strings.TrimSpace(o.Industry) == "":
			missingIndustry++
		case !finite(o.Size) || o.Size <= 0:
			missingSize++
		default:
			idx = append(idx, i)
		}
	}
	dropped += missingIndustry + missingSize
	notes := make([]string, 0, 3)
	if missingIndustry > 0 {
		notes = append(notes, "行业缺失丢弃: "+itoa(missingIndustry))
	}
	if missingSize > 0 {
		notes = append(notes, "市值缺失丢弃: "+itoa(missingSize))
	}
	sort.Slice(idx, func(a, b int) bool { return obs[idx[a]].Code < obs[idx[b]].Code })
	if len(idx) == 0 {
		return NeutralizationResult{Status: neutralizationStatusUnavailable, Reason: "无有效暴露观测", Dropped: dropped, Notes: notes}
	}

	vals := applyWinsorization(valuesAt(obs, idx), spec, &notes)
	std, reason := standardizeCrossSection(vals, spec.Standardization)
	if reason != "" {
		return NeutralizationResult{Status: neutralizationStatusUnavailable, Reason: reason, Dropped: dropped, Notes: notes}
	}

	// 设计矩阵：截距 + (K-1) 个行业 dummy（丢弃字母序首个行业，避免哑变量
	// 陷阱）+ log(市值)。行业与市值必须由调用方按 AsOf 从 PIT View 读取；
	// 用当前数据回填历史属于 unverified，由协议层声明。
	sortedInd := distinctSortedIndustries(obs, idx)
	colOf := make(map[string]int, len(sortedInd))
	for i, ind := range sortedInd[1:] {
		colOf[ind] = i + 1 // 列 0 为截距
	}
	k := len(sortedInd) + 1 // 截距 + (K-1) dummy + log size

	n := len(idx)
	if n < minRegressionSamples || n < 2*k {
		need := max(minRegressionSamples, 2*k)
		return NeutralizationResult{
			Status: neutralizationStatusUnavailable, Reason: "样本不足: " + itoa(n) + " < " + itoa(need),
			Dropped: dropped, Notes: notes,
		}
	}

	X := make([][]float64, n)
	for r, i := range idx {
		row := make([]float64, k)
		row[0] = 1
		if c, ok := colOf[obs[i].Industry]; ok {
			row[c] = 1
		}
		row[k-1] = math.Log(obs[i].Size)
		X[r] = row
	}
	resid, ok := olsResiduals(X, std)
	if !ok {
		return NeutralizationResult{Status: neutralizationStatusUnavailable, Reason: "设计矩阵奇异（共线）", Dropped: dropped, Notes: notes}
	}
	return NeutralizationResult{Status: neutralizationStatusOK, Values: zipValues(obs, idx, resid), Used: n, Dropped: dropped, Notes: notes}
}

// sortedValidIndexes 返回基础有效（代码非空且因子值有限）的观测索引，按代码
// 稳定排序（顺序不变性的基础）。
func sortedValidIndexes(obs []ExposureObservation) []int {
	idx := make([]int, 0, len(obs))
	for i, o := range obs {
		if strings.TrimSpace(o.Code) == "" || !finite(o.Value) {
			continue
		}
		idx = append(idx, i)
	}
	sort.Slice(idx, func(a, b int) bool { return obs[idx[a]].Code < obs[idx[b]].Code })
	return idx
}

func valuesAt(obs []ExposureObservation, idx []int) []float64 {
	out := make([]float64, len(idx))
	for r, i := range idx {
		out[r] = obs[i].Value
	}
	return out
}

func distinctSortedIndustries(obs []ExposureObservation, idx []int) []string {
	seen := make(map[string]bool, len(idx))
	for _, i := range idx {
		seen[obs[i].Industry] = true
	}
	out := make([]string, 0, len(seen))
	for ind := range seen {
		out = append(out, ind)
	}
	sort.Strings(out)
	return out
}

// zipValues 按索引把输出值装配为 code → value。
func zipValues(obs []ExposureObservation, idx []int, vals []float64) map[string]float64 {
	out := make(map[string]float64, len(idx))
	for r, i := range idx {
		out[obs[i].Code] = vals[r]
	}
	return out
}

// applyWinsorization 可选 MAD 去极值；MAD 为零（过半样本同值）时跳过并披露。
func applyWinsorization(vals []float64, spec NeutralizationSpec, notes *[]string) []float64 {
	if spec.Winsorization != winsorizationMAD || len(vals) < 2 {
		return vals
	}
	med := median(vals)
	dev := make([]float64, len(vals))
	for i, v := range vals {
		dev[i] = math.Abs(v - med)
	}
	mad := median(dev)
	if mad == 0 {
		*notes = append(*notes, "MAD 为零，跳过去极值")
		return vals
	}
	hi := med + madWinsorThreshold*madConsistencyScale*mad
	lo := med - madWinsorThreshold*madConsistencyScale*mad
	out := make([]float64, len(vals))
	for i, v := range vals {
		out[i] = min(max(v, lo), hi)
	}
	return out
}

// standardizeCrossSection 截面标准化。zscore 用样本均值与样本标准差；rank 用
// 平均秩对均匀分布理论矩标准化（并列值取平均秩，与分组"并列块不拆分"一致）。
// 常数序列或样本不足时返回原因，不返回全零。
func standardizeCrossSection(vals []float64, method string) ([]float64, string) {
	n := len(vals)
	if n < 2 {
		return nil, "样本不足: " + itoa(n)
	}
	switch method {
	case standardizationZScore:
		mean := 0.0
		for _, v := range vals {
			mean += v
		}
		mean /= float64(n)
		ss := 0.0
		for _, v := range vals {
			ss += (v - mean) * (v - mean)
		}
		std := math.Sqrt(ss / float64(n-1))
		if !(std > 0) || math.IsInf(std, 0) {
			return nil, "常数序列，无截面信息"
		}
		out := make([]float64, n)
		for i, v := range vals {
			out[i] = (v - mean) / std
		}
		return out, ""
	case standardizationRank:
		ranks := averageRanks(vals)
		scale := math.Sqrt((float64(n*n) - 1) / 12) // 均匀秩的理论标准差
		out := make([]float64, n)
		for i, r := range ranks {
			out[i] = (r - float64(n+1)/2) / scale
		}
		return out, ""
	default:
		return nil, "标准化方式非法: " + method
	}
}

// averageRanks 平均秩（1 起）：并列值取算术平均秩。
func averageRanks(vals []float64) []float64 {
	n := len(vals)
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return vals[idx[a]] < vals[idx[b]] })
	ranks := make([]float64, n)
	for i := 0; i < n; {
		j := i
		for j+1 < n && vals[idx[j+1]] == vals[idx[i]] {
			j++
		}
		avg := float64(i+j+2) / 2 // 位置 i..j 的平均秩（秩从 1 起）
		for t := i; t <= j; t++ {
			ranks[idx[t]] = avg
		}
		i = j + 1
	}
	return ranks
}

// olsResiduals 最小二乘残差：正规方程 + 列部分主元高斯消元。矩阵奇异（共线）
// 返回 false。仅用于小截面设计（列数 = 行业数 + 2）。
func olsResiduals(X [][]float64, y []float64) ([]float64, bool) {
	n := len(y)
	k := len(X[0])
	// A = [X'X | X'y] 增广矩阵。
	A := make([][]float64, k)
	scale := 0.0
	for i := range A {
		A[i] = make([]float64, k+1)
		for j := 0; j <= i; j++ {
			s := 0.0
			for r := 0; r < n; r++ {
				s += X[r][i] * X[r][j]
			}
			A[i][j], A[j][i] = s, s
			scale = max(scale, math.Abs(s))
		}
		s := 0.0
		for r := 0; r < n; r++ {
			s += X[r][i] * y[r]
		}
		A[i][k] = s
	}
	tol := olsSingularityRelTol * scale
	for col := 0; col < k; col++ {
		piv := col
		for r := col + 1; r < k; r++ {
			if math.Abs(A[r][col]) > math.Abs(A[piv][col]) {
				piv = r
			}
		}
		if math.Abs(A[piv][col]) <= tol {
			return nil, false
		}
		A[col], A[piv] = A[piv], A[col]
		for r := col + 1; r < k; r++ {
			f := A[r][col] / A[col][col]
			for c := col; c <= k; c++ {
				A[r][c] -= f * A[col][c]
			}
		}
	}
	beta := make([]float64, k)
	for row := k - 1; row >= 0; row-- {
		s := A[row][k]
		for c := row + 1; c < k; c++ {
			s -= A[row][c] * beta[c]
		}
		if math.Abs(A[row][row]) <= tol {
			return nil, false
		}
		beta[row] = s / A[row][row]
	}
	resid := make([]float64, n)
	for r := 0; r < n; r++ {
		fit := 0.0
		for c := 0; c < k; c++ {
			fit += X[r][c] * beta[c]
		}
		resid[r] = y[r] - fit
	}
	return resid, true
}

func median(vals []float64) float64 {
	s := append([]float64(nil), vals...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
