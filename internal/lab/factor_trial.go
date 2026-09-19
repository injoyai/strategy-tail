package lab

import (
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"time"
)

// factor_trial.go 追加式试验账本领域层（设计 §10.1，计划 Task 6 Step 1/4）。
// 专业模式每次 v4 分析都登记一条 trial 记录；失败、取消和无样本运行同样保留，
// 不允许"只记录成功结果"的生存者偏差账本。本文件只承载结构、状态与纯函数；
// 存储见 factor_trial_store.go，分析集成见 factor_analysis_v4.go。

// TrialStatus 试验状态五态。running 仅存在于请求文件（Start 后、Finish 前）；
// 结果文件只允许其余四态。
type TrialStatus string

const (
	TrialStatusRunning      TrialStatus = "running"
	TrialStatusCompleted    TrialStatus = "completed"
	TrialStatusFailed       TrialStatus = "failed"
	TrialStatusCanceled     TrialStatus = "canceled"
	TrialStatusInsufficient TrialStatus = "insufficient" // 运行成功但无有效标签样本
)

// finishedTrialStatuses Finish 允许写入的状态白名单：running 不是完成态。
var finishedTrialStatuses = map[TrialStatus]bool{
	TrialStatusCompleted:    true,
	TrialStatusFailed:       true,
	TrialStatusCanceled:     true,
	TrialStatusInsufficient: true,
}

// trialIDRe 受限试验 ID：tr_<UTC yyyyMMddTHHmmssSSSZ>_<8 hex>，与分析 ID 同构。
var trialIDRe = regexp.MustCompile(`^tr_\d{8}T\d{9}Z_[0-9a-f]{8}$`)

// newTrialID 生成不可变试验 ID；random 为 nil 时使用 crypto/rand.Reader。
func newTrialID(now time.Time, random io.Reader) (string, error) {
	return newPrefixedID("tr", now, random)
}

// validTrialID 严格校验试验 ID 格式（同时拒绝路径穿越）。
func validTrialID(id string) bool { return trialIDRe.MatchString(id) }

// FactorTrial 一次专业模式运行的账本记录（设计 §10.1）。请求与结果分文件
// 存储：Start 写请求（status=running，永不修改），Finish 写结果；完整 trial
// 由二者组合读取。不保存用户可编辑名称。
type FactorTrial struct {
	ID           string         `json:"id"`
	FamilyID     string         `json:"familyId"`
	VariantID    string         `json:"variantId"`
	AnalysisID   string         `json:"analysisId"`
	Factor       FactorSnapshot `json:"factor"`
	ProtocolHash string         `json:"protocolHash"`
	StartedAt    string         `json:"startedAt"`
	FinishedAt   string         `json:"finishedAt"`
	Status       TrialStatus    `json:"status"`
	// Message 完成/失败说明（失败原因、取消或无样本说明）；来自结果文件。
	Message string `json:"message,omitempty"`
}

// TrialResult 试验完成记录：Finish 时写入独立结果文件，与请求文件组合成
// 完整 trial。FinishedAt 由 Store 以服务端时间戳填充，忽略调用方传入值。
type TrialResult struct {
	Status     TrialStatus `json:"status"`
	FinishedAt string      `json:"finishedAt"`
	Message    string      `json:"message,omitempty"`
}

// benjaminiHochberg 对同一家族的 p 值计算 Benjamini-Hochberg q 值（FDR 控制）。
// 仅作为家族披露辅助，不得单独用作自动晋级条件（设计 §10）。
// 输入与输出按位置对应；p 值为空切片时返回空切片。负数、大于 1 或 NaN 的
// p 值返回错误（fail closed），不静默截断。
func benjaminiHochberg(ps []float64) ([]float64, error) {
	n := len(ps)
	for i, p := range ps {
		if math.IsNaN(p) || p < 0 || p > 1 {
			return nil, fmt.Errorf("p 值非法: ps[%d]=%v（应为 [0,1]）", i, p)
		}
	}
	out := make([]float64, n)
	if n == 0 {
		return out, nil
	}
	// 按升序取得原下标次序；BH 允许并列 p 值。
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return ps[idx[a]] < ps[idx[b]] })
	// 自大到小 step-up：q_(k) = min over j>=k of p_(j) * n / j，并截断到 1。
	q := 1.0
	for k := n; k >= 1; k-- {
		q = math.Min(q, ps[idx[k-1]]*float64(n)/float64(k))
		out[idx[k-1]] = q
	}
	return out, nil
}
