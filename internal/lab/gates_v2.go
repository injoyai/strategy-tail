package lab

// gates_v2.go v2 多因子组合研究发布门禁评估（实施计划 Task 12 发布门禁）。
//
// 本文件只定义发布门禁的纯函数：输入 = 证据集合 + 检查结果（由
// release_v2_test.go 运行既有测试 / 读取文件后构造），输出 = 门禁结果表与
// 建议发布决定。
//
// 门禁评估是只读检查：不写产物、不改变任何状态；release 测试失败 = 发布被
// 阻止，但已写产物与已发布状态不受影响（该语义文档化在
// doc/v2-multifactor-portfolio.md §发布门禁语义）。

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
)

// ReleaseGateStatus 发布门禁单项状态：pass | fail | not_applicable。
type ReleaseGateStatus string

const (
	// ReleaseGatePass 门禁通过。
	ReleaseGatePass ReleaseGateStatus = "pass"
	// ReleaseGateFail 门禁失败（未解释则阻止发布）。
	ReleaseGateFail ReleaseGateStatus = "fail"
	// ReleaseGateNotApplicable 门禁不适用（必须附 Notes 解释，如无预设阈值仅记录）。
	ReleaseGateNotApplicable ReleaseGateStatus = "not_applicable"
)

// ReleaseGateCategory 发布门禁类别（六类：可迁移性/正确性/可重复性/审计/性能/文档）。
type ReleaseGateCategory string

const (
	// GateCategoryMigration 可迁移性：v1 不可变历史不受影响。
	GateCategoryMigration ReleaseGateCategory = "可迁移性"
	// GateCategoryCorrectness 正确性：金标准/会计恒等式/确定性。
	GateCategoryCorrectness ReleaseGateCategory = "正确性"
	// GateCategoryRepeatable 可重复性：重复运行与重启读取。
	GateCategoryRepeatable ReleaseGateCategory = "可重复性"
	// GateCategoryAudit 审计：manifest/后端结论/hash/路径安全。
	GateCategoryAudit ReleaseGateCategory = "审计"
	// GateCategoryPerformance 性能：基准绝对值记录。
	GateCategoryPerformance ReleaseGateCategory = "性能"
	// GateCategoryDocs 文档：发布文档与限制记录。
	GateCategoryDocs ReleaseGateCategory = "文档"
)

// ReleaseCheck 单项门禁检查结果（三态）。
type ReleaseCheck int

const (
	// CheckPass 检查通过。
	CheckPass ReleaseCheck = iota
	// CheckFail 检查失败（未解释 → 阻止发布）。
	CheckFail
	// CheckNotApplicable 检查不适用（必须附 Notes 解释）。
	CheckNotApplicable
)

// ReleaseCheckInput 单项门禁的检查结果与证据（由调用方从既有测试通过性 /
// 文件存在性 / 性能基准收集）。
type ReleaseCheckInput struct {
	// Name 门禁名（与 releaseGateDefs 清单一致）。
	Name string
	// Check 检查结果。
	Check ReleaseCheck
	// Evidence 证据：测试名或命令 + 输出摘要。
	Evidence string
	// Notes 说明（not_applicable 必填；其余可选）。
	Notes string
}

// ReleaseGateResult 发布门禁单项结果。
type ReleaseGateResult struct {
	Category ReleaseGateCategory `json:"category"`
	Name     string              `json:"name"`
	Status   ReleaseGateStatus   `json:"status"`
	Evidence string              `json:"evidence"`
	Notes    string              `json:"notes,omitempty"`
}

// ReleaseGateDef 规范发布门禁项定义（固定清单，发布文档门禁表与此一一对应）。
type ReleaseGateDef struct {
	Category ReleaseGateCategory
	Name     string
	Purpose  string
}

// releaseGateDefs 发布门禁规范清单（固定顺序；Task 12 门禁六类全覆盖）。
var releaseGateDefs = []ReleaseGateDef{
	{Category: GateCategoryMigration, Name: "v1_products_readable",
		Purpose: "v1 不可变历史不受影响：factor_validation/candidate/analysis store 既有产物与测试全部可读可测"},
	{Category: GateCategoryMigration, Name: "legacy_api_compatible",
		Purpose: "旧客户端 API 兼容：/api/status、/api/run、/api/script、/api/factors、候选/策略路由行为不变"},
	{Category: GateCategoryCorrectness, Name: "e2e_golden_case",
		Purpose: "端到端金标准：Task 11 固定案例全链手算断言 + 真实 runner 全链 + 泄漏复证通过"},
	{Category: GateCategoryCorrectness, Name: "accounting_identity",
		Purpose: "会计恒等式：每日对账恒等式 + 归因加总成立"},
	{Category: GateCategoryCorrectness, Name: "determinism",
		Purpose: "确定性：同输入两次运行指标/归因/净值/CSV 逐位一致"},
	{Category: GateCategoryRepeatable, Name: "repeat_hash_consistent",
		Purpose: "可重复性：同输入重复运行产物 hash 一致（时间戳字段排除并文档化）"},
	{Category: GateCategoryRepeatable, Name: "restart_hash_consistent",
		Purpose: "可重复性：重启后 Store 读取 hash 重算校验一致（篡改 fail closed）"},
	{Category: GateCategoryAudit, Name: "completed_has_manifest",
		Purpose: "审计：completed 必有完整五类产物 manifest，绝无 completed 但产物不完整"},
	{Category: GateCategoryAudit, Name: "verdict_backend_generated",
		Purpose: "审计：验证 verdict 由后端按冻结门禁生成，客户端不能指定；读取重算比对"},
	{Category: GateCategoryAudit, Name: "artifacts_hashed",
		Purpose: "审计：产物含 SHA-256 hash（manifest/报告 hash），读取 fail closed"},
	{Category: GateCategoryAudit, Name: "no_path_traversal",
		Purpose: "审计：产物名/ID 白名单拒绝路径穿越（含 URL 编码/Unicode 变体）"},
	{Category: GateCategoryPerformance, Name: "benchmark_recorded",
		Purpose: "性能：记录基准绝对值（时长/峰值内存/产物大小）+ 机器环境（无预设阈值仅记录）"},
	{Category: GateCategoryDocs, Name: "docs_complete",
		Purpose: "文档：UX-CONTRACT.md、DESIGN.md §7、doc/v2-multifactor-portfolio.md 存在且记录限制"},
}

// ReleaseGateDefs 返回发布门禁规范清单副本（供文档/测试呈现）。
func ReleaseGateDefs() []ReleaseGateDef {
	return append([]ReleaseGateDef(nil), releaseGateDefs...)
}

// gateResultOf 由检查结果构造门禁结果。
func gateResultOf(def ReleaseGateDef, in ReleaseCheckInput) ReleaseGateResult {
	status := ReleaseGatePass
	switch in.Check {
	case CheckFail:
		status = ReleaseGateFail
	case CheckNotApplicable:
		status = ReleaseGateNotApplicable
	}
	return ReleaseGateResult{
		Category: def.Category,
		Name:     def.Name,
		Status:   status,
		Evidence: in.Evidence,
		Notes:    in.Notes,
	}
}

// EvaluateReleaseGates 评估发布门禁（纯函数）：按规范清单固定顺序输出门禁
// 结果表；未提供检查结果的项 → fail（"未解释"阻止发布）。
func EvaluateReleaseGates(inputs []ReleaseCheckInput) []ReleaseGateResult {
	byName := make(map[string]ReleaseCheckInput, len(inputs))
	for _, in := range inputs {
		byName[in.Name] = in
	}
	gates := make([]ReleaseGateResult, 0, len(releaseGateDefs))
	for _, def := range releaseGateDefs {
		in, ok := byName[def.Name]
		if !ok {
			in = ReleaseCheckInput{Name: def.Name, Check: CheckFail, Evidence: "无", Notes: "未提供检查结果（未解释）"}
		}
		gates = append(gates, gateResultOf(def, in))
	}
	return gates
}

// ReleaseRecommendation 建议发布决定。
type ReleaseRecommendation string

const (
	// RecommendPass 建议发布。
	RecommendPass ReleaseRecommendation = "PASS"
	// RecommendConditional 有条件发布（条件 = 接受已记录限制与不适用项）。
	RecommendConditional ReleaseRecommendation = "CONDITIONAL"
	// RecommendBlock 阻止发布。
	RecommendBlock ReleaseRecommendation = "BLOCK"
)

// ReleaseCandidate 发布候选冻结：候选 hash、门禁结果表、限制清单、建议发布决定。
type ReleaseCandidate struct {
	Hash           string                `json:"hash"`
	CodeVersion    string                `json:"codeVersion"`
	FrozenAt       string                `json:"frozenAt"`
	Gates          []ReleaseGateResult   `json:"gates"`
	Limitations    []string              `json:"limitations"`
	Recommendation ReleaseRecommendation `json:"recommendation"`
	Reason         string                `json:"reason"`
}

// recommendationOf 由门禁结果表与限制清单计算建议发布决定（纯函数）。
func recommendationOf(gates []ReleaseGateResult, limitations []string) ReleaseRecommendation {
	for _, g := range gates {
		if g.Status == ReleaseGateFail {
			return RecommendBlock
		}
	}
	if len(limitations) > 0 {
		return RecommendConditional
	}
	for _, g := range gates {
		if g.Status == ReleaseGateNotApplicable {
			return RecommendConditional
		}
	}
	return RecommendPass
}

// reasonText 生成建议发布决定的理由（纯函数）。
func reasonText(rec ReleaseRecommendation, gates []ReleaseGateResult, limitations []string) string {
	var fails, nas []string
	for _, g := range gates {
		switch g.Status {
		case ReleaseGateFail:
			fails = append(fails, g.Name)
		case ReleaseGateNotApplicable:
			nas = append(nas, g.Name)
		}
	}
	switch rec {
	case RecommendBlock:
		return fmt.Sprintf("存在未通过门禁：%s（未解释则阻止发布）", strings.Join(fails, "、"))
	case RecommendConditional:
		parts := []string{"全部门禁无 fail"}
		if len(nas) > 0 {
			parts = append(parts, fmt.Sprintf("；不适用项 %s 已解释（无预设阈值，仅记录绝对值）", strings.Join(nas, "、")))
		}
		if len(limitations) > 0 {
			parts = append(parts, fmt.Sprintf("；已记录并接受 %d 项限制（发布文档 §限制 逐项披露）", len(limitations)))
		}
		parts = append(parts, "；条件 = 在文档披露的边界内使用（涨跌停/停牌未建模、基准未接入等），后续阶段补齐")
		return strings.Join(parts, "")
	case RecommendPass:
		return "全部门禁通过且无已记录限制"
	}
	return ""
}

// FreezeReleaseCandidate 冻结发布候选（纯函数，不写盘）。
// 建议决定规则：
//   - 任一门禁 fail → BLOCK（未解释项阻止发布）；
//   - 全部 pass 且无 not_applicable、限制清单为空 → PASS；
//   - 其余（存在 not_applicable 或已记录限制）→ CONDITIONAL（条件 = 接受
//     已记录限制，发布文档逐项披露）。
func FreezeReleaseCandidate(hash, codeVersion, frozenAt string, gates []ReleaseGateResult, limitations []string) ReleaseCandidate {
	rec := recommendationOf(gates, limitations)
	return ReleaseCandidate{
		Hash:           hash,
		CodeVersion:    codeVersion,
		FrozenAt:       frozenAt,
		Gates:          append([]ReleaseGateResult(nil), gates...),
		Limitations:    append([]string(nil), limitations...),
		Recommendation: rec,
		Reason:         reasonText(rec, gates, limitations),
	}
}

// KeyFileHash 发布候选关键文件指纹（相对仓库路径 + SHA-256）。
type KeyFileHash struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// ReleaseCandidateHash 由关键文件指纹列表计算发布候选冻结 hash（纯函数）：
// 按路径字典序排序 → 逐条 "path:sha256" 拼接 → SHA-256。路径参与 hash，
// 关键文件清单变更会使候选 hash 变化（候选身份随代码冻结）。
func ReleaseCandidateHash(files []KeyFileHash) string {
	sorted := append([]KeyFileHash(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	h := sha256.New()
	for _, f := range sorted {
		_, _ = io.WriteString(h, f.Path+":"+f.SHA256+"\n")
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ReleaseLimitations 已接受剩余风险与限制清单（Task 1-11 审计记录 + Task 12
// 门禁输入；发布文档 §限制 与此一致）。门禁评估不修改任何产物或状态。
var ReleaseLimitations = []string{
	"涨跌停/停牌未建模：runner 按股票池成员判定可交易性，不读涨跌停/停牌状态（执行层 DayMarket 已具备 LimitUp/LimitDown/Tradable 能力，数据接入未落地）",
	"市值/行业数据依赖：行业上限启用需要行业映射（数据源未接入，选中股票缺行业 fail closed）；市值中性化/市值因子依赖本地估值库，v2 尚未接入",
	"市场阶段/滚动窗口稳定性、行业/市值集中度门禁、单因子组合增量比较未落地（Task 6/9/10 记录；门禁阈值可配置但这些统计未接入验证报告）",
	"基准未接入：Benchmark.Available=false；缺基准不阻止绝对收益报告，但 IR 门禁缺基准/对齐不足时 fail（无法判定超额，语义文档化）",
	"归因限制：v2 归因是统计分解（收益来源的算术/复利分解），不是因果证明，也不是完整风险模型归因；净值口径与算术累计存在复利交叉项/成交时点/整手偏离残差（显式披露，不静默抹平）",
	"target.go：无任何单票/行业上限配置时，可交易性剔除后释放权重不再分配（capSolve 仅在至少一个上限启用时水填充），随后 StepTarget 权重和校验失败 → 结构化 insufficient（fail closed 不静默放宽）；该实现与设计 §8.2 注释在无上限场景不一致，已测锁定（Minor 观察项）",
	"portfolio_runner FinishedAt 用 time.Now：report.json 的 startedAt/finishedAt 是运行时刻信息（确定性字段外差异）；CSV 产物、指标、归因、净值逐位一致（TestAuditE2EDeterminismAndRestart 锁定）",
	"cmd/valuation-sync：-codes 分支未显式初始化运行时（上游遗留失败，需差分归因；-all 分支已显式 common.Initialize()）",
	"模型列表筛选/排序为当前页内（GET /api/factor-models 无分页参数，前端本地分页并写 URL page）；无模型归档/恢复 HTTP 路由；无 v1 验证列表路由（证据区聚合已有模型的 validatedFactors）",
	"滚动 IC 训练窗 = 视图日期最后 windowYears×250 天（交易日近似），非自然年窗口；训练窗内数据不足时按冻结 fallback（等权/现金）不临时择优",
	"性能无预设阈值：20 因子 × 300 股票 × 750 交易日 ≈ 2.9s / 峰值内存 1215MB（HeapAlloc 采样）/ 产物 ≈2.77MB（report.json + 四类 CSV 估算），仅记录绝对值，机器相关（Task 11 基准，本机实测为准）",
}
