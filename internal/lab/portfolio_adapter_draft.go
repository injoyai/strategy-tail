package lab

// portfolio_adapter_draft.go — v2 Task 0 动作 3 适配层接口草案（计划
// 2026-09-18-multifactor-portfolio-v2 §Task 0）。
//
// 契约：v2 的 internal/portfolioresearch（Task 1 创建）只能消费本文件定义的
// 类型化视图（ValidationView / ValidationSummary），禁止直接解析 v1 JSON
// 文件。当前实现先把 ValidationStore 封装为 ValidationInput；Task 1 将按
// 相同契约把适配层迁移到 portfolioresearch 包内。
//
// 与正式契约的边界见 docs/superpowers/plans/2026-09-18-multifactor-portfolio-v2-task0-admission.md：
// 冻结记录（ValidationRequest）、验证报告（FactorValidationReport）、窗口
// 报告（ValidationWindowReport）与摘要（ValidationSummary）为正式契约；
// 存储目录布局、request.json/report.json/windows/NNNN.json 等文件名是当前
// 实现细节，调用方不得依赖。

// ValidationInput 组合层读取验证结果的稳定契约。实现方负责解析与校验 v1
// 存储格式；调用方只消费类型化视图，不感知 v1 JSON 布局。
type ValidationInput interface {
	// Get 读取单个验证详情（冻结请求 + 派生状态 + 窗口进度 + 终态报告）。
	// 记录缺失返回 errValidationNotFound；损坏记录 fail closed 返回错误。
	Get(id string) (ValidationView, error)
	// List 按筛选条件列出验证摘要（State/EvidenceClass 白名单枚举，未知值
	// 拒绝）。排序：FrozenAt 倒序，ID 升序兜底。
	List(filter ValidationFilter) ([]ValidationSummary, error)
}

// ValidationStoreAdapter 将 ValidationStore 暴露为 ValidationInput。
var _ ValidationInput = (*ValidationStoreAdapter)(nil)

type ValidationStoreAdapter struct {
	store *ValidationStore
}

// NewValidationStoreAdapter 包装现有 ValidationStore（同一 root 目录）。
func NewValidationStoreAdapter(store *ValidationStore) *ValidationStoreAdapter {
	return &ValidationStoreAdapter{store: store}
}

// Get 见 ValidationInput.Get。
func (a *ValidationStoreAdapter) Get(id string) (ValidationView, error) {
	return a.store.Get(id)
}

// List 见 ValidationInput.List。
func (a *ValidationStoreAdapter) List(filter ValidationFilter) ([]ValidationSummary, error) {
	return a.store.List(filter)
}
