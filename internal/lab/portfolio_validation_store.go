package lab

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/injoyai/strategy-tail/internal/portfolioresearch"
)

// portfolio_validation_store.go v2 Task 8 组合验证存储（设计 §12.1/§11）。
//
// 目录布局（root 生产默认 data/lab/portfolio-validations）：
//
//	<root>/<validation-id>/
//	  request.json        # 冻结主记录：Create 先发布，之后永不修改
//	  report.json         # 最终报告：Complete 一次写入，完成标记
//	  windows/NNNN.json   # 窗口结果：完成前可重写（崩溃恢复），完成后拒绝
//
// 契约：
//   - 创建时冻结 spec（幂等：同 RequestID 同请求 hash 返回已有记录；模型
//     revision 由 PortfolioModelSource 读取并校验 modelHash，fail closed）；
//   - 服务端 ID pv_<ts>_<hex>；spec hash 进入记录，读取时重算不匹配
//     fail closed；
//   - 结论由后端生成：Complete 用冻结 spec 对窗口结果求值（Evaluate），
//     原子写报告，只允许一次完成；读取 completed 记录时重算 verdict +
//     证据等级与存储一致，不匹配 fail closed；
//   - 修改模型或门禁 → 新验证 ID（spec hash 变化）；旧验证保留并经由新记录
//     Supersedes 派生 SupersededBy（旧测试窗视为已见数据）；
//   - 原子写、withinRoot 路径检查、损坏 fail closed；崩溃残留 .tmp-* 忽略。

// DefaultPortfolioValidationRoot 生产默认组合验证目录。
func DefaultPortfolioValidationRoot() string {
	return filepath.Join("data", "lab", "portfolio-validations")
}

const (
	// DefaultPortfolioValidationPageSize 列表默认每页条数。
	DefaultPortfolioValidationPageSize = 20
	// MaxPortfolioValidationPageSize 列表每页条数上限（资源保护）。
	MaxPortfolioValidationPageSize = 200
)

// PortfolioValidationStore 组合验证追加式存储。
type PortfolioValidationStore struct {
	root string
	mu   sync.Mutex
	now  func() time.Time
}

// NewPortfolioValidationStore 创建组合验证存储；测试必须使用 t.TempDir()。
func NewPortfolioValidationStore(root string) *PortfolioValidationStore {
	return &PortfolioValidationStore{root: root, now: time.Now}
}

func (s *PortfolioValidationStore) requestPath(id string) string {
	return filepath.Join(s.root, id, "request.json")
}

func (s *PortfolioValidationStore) reportPath(id string) string {
	return filepath.Join(s.root, id, "report.json")
}

func (s *PortfolioValidationStore) windowsDir(id string) string {
	return filepath.Join(s.root, id, "windows")
}

// validationDirs 扫描根目录下全部合法验证目录（不排序）。
func (s *PortfolioValidationStore) validationDirs() ([]string, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() && validPortfolioValidationID(e.Name()) {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}

// readRequestLocked 读取并完整校验冻结主记录：schema、ID 与目录一致、
// spec hash 重算一致、规格合法、模型引用格式、证据等级/幂等键/被替换 ID
// 合法。损坏 fail closed。
func (s *PortfolioValidationStore) readRequestLocked(id string) (PortfolioValidationRecord, error) {
	path := s.requestPath(id)
	if !withinRoot(s.root, path) {
		return PortfolioValidationRecord{}, fmt.Errorf("验证 ID 派生路径越界")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return PortfolioValidationRecord{}, errPortfolioValidationNotFound
		}
		return PortfolioValidationRecord{}, err
	}
	var rec PortfolioValidationRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return PortfolioValidationRecord{}, fmt.Errorf("组合验证请求损坏: %w", err)
	}
	if rec.SchemaVersion != portfolioValidationSchemaVersion {
		return PortfolioValidationRecord{}, fmt.Errorf("未知组合验证 schema 版本: %d", rec.SchemaVersion)
	}
	if rec.ID != id {
		return PortfolioValidationRecord{}, fmt.Errorf("验证 ID 与目录不一致")
	}
	hash, err := portfolioresearch.PortfolioValidationSpecHash(rec.Spec)
	if err != nil {
		return PortfolioValidationRecord{}, err
	}
	if hash != rec.SpecHash {
		return PortfolioValidationRecord{}, fmt.Errorf("验证规格哈希不匹配（记录不可信，fail closed）")
	}
	if err := rec.Spec.Validate(); err != nil {
		return PortfolioValidationRecord{}, fmt.Errorf("验证规格非法: %w", err)
	}
	if !validModelID(rec.Spec.ModelRef.ModelID) {
		return PortfolioValidationRecord{}, fmt.Errorf("冻结模型 ID 非法: %q", rec.Spec.ModelRef.ModelID)
	}
	if !portfolioresearch.ValidEvidenceClass(rec.ModelEvidenceClass) {
		return PortfolioValidationRecord{}, fmt.Errorf("模型证据等级非法: %q", rec.ModelEvidenceClass)
	}
	if !validUUID(rec.RequestID) {
		return PortfolioValidationRecord{}, fmt.Errorf("幂等键非法: %q", rec.RequestID)
	}
	if !hashHexRe.MatchString(rec.RequestHash) {
		return PortfolioValidationRecord{}, fmt.Errorf("幂等请求哈希非法: %q", rec.RequestHash)
	}
	if rec.Supersedes != "" && !validPortfolioValidationID(rec.Supersedes) {
		return PortfolioValidationRecord{}, fmt.Errorf("被替换验证 ID 非法: %q", rec.Supersedes)
	}
	return rec, nil
}

// Create 冻结 spec 并创建验证（幂等）。models 读取冻结模型 revision 并
// 校验 modelHash（客户端声明的模型 hash 必须与存储一致，fail closed），
// 证据等级由后端从模型派生。request.json 先发布且永不修改。
func (s *PortfolioValidationStore) Create(req CreatePortfolioValidationRequest, models PortfolioModelSource) (PortfolioValidationRecord, bool, error) {
	norm, err := normalizeCreatePortfolioValidationRequest(req)
	if err != nil {
		return PortfolioValidationRecord{}, false, err
	}
	reqHash, err := createPortfolioValidationRequestHash(norm)
	if err != nil {
		return PortfolioValidationRecord{}, false, err
	}
	if models == nil {
		return PortfolioValidationRecord{}, false, fmt.Errorf("模型存储缺失（冻结模型引用需要按 revision 读取并校验 modelHash）")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 幂等扫描：RequestID 命中且请求 hash 一致 → 返回已有记录。
	existing, err := s.findByRequestIDLocked(norm.RequestID)
	if err != nil {
		return PortfolioValidationRecord{}, false, err
	}
	if existing != nil {
		if existing.RequestHash == reqHash {
			return *existing, false, nil
		}
		return PortfolioValidationRecord{}, false, errIdempotencyConflict
	}

	// 冻结模型引用：按 revision 读取并校验 modelHash（fail closed）。
	m, err := models.Get(norm.Spec.ModelRef.ModelID, norm.Spec.ModelRef.Revision)
	if err != nil {
		return PortfolioValidationRecord{}, false, fmt.Errorf("读取冻结模型 %s@%d 失败: %w",
			norm.Spec.ModelRef.ModelID, norm.Spec.ModelRef.Revision, err)
	}
	if m.ModelHash != norm.Spec.ModelRef.Hash {
		return PortfolioValidationRecord{}, false, fmt.Errorf(
			"模型 hash 不匹配: 请求 %s ≠ 存储 %s（fail closed，须以模型存储为准）", norm.Spec.ModelRef.Hash, m.ModelHash)
	}

	specHash, err := portfolioresearch.PortfolioValidationSpecHash(norm.Spec)
	if err != nil {
		return PortfolioValidationRecord{}, false, err
	}
	id, err := newPortfolioValidationID(s.now(), nil)
	if err != nil {
		return PortfolioValidationRecord{}, false, fmt.Errorf("生成验证 ID 失败: %w", err)
	}
	rec := PortfolioValidationRecord{
		SchemaVersion:      portfolioValidationSchemaVersion,
		ID:                 id,
		CreatedAt:          s.now().UTC().Format(time.RFC3339),
		Spec:               norm.Spec,
		SpecHash:           specHash,
		ModelEvidenceClass: m.EvidenceClass,
		RequestID:          norm.RequestID,
		RequestHash:        reqHash,
		Supersedes:         norm.Supersedes,
	}

	dir := filepath.Join(s.root, id)
	if !withinRoot(s.root, dir) {
		return PortfolioValidationRecord{}, false, fmt.Errorf("验证 ID 派生路径越界")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return PortfolioValidationRecord{}, false, err
	}
	buf, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return PortfolioValidationRecord{}, false, err
	}
	if err := atomicWrite(s.requestPath(id), buf); err != nil {
		return PortfolioValidationRecord{}, false, err
	}
	return rec, true, nil
}

// SaveWindow 写入单个窗口结果。验证必须存在且未完成（report.json 是完成
// 标记）；完成前允许重写同一窗口（崩溃恢复重跑属进度语义），完成后拒绝。
// Index 1~9999 且与文件名一致；窗口结果 ID 必须与验证一致。
func (s *PortfolioValidationStore) SaveWindow(id string, w portfolioresearch.ValidationWindowOutcome) error {
	if !validPortfolioValidationID(id) {
		return fmt.Errorf("非法验证 ID: %q", id)
	}
	if w.Index < 1 || w.Index > 9999 {
		return fmt.Errorf("窗口序号无效: %d（应为 1-9999）", w.Index)
	}
	if w.ValidationID != id {
		return fmt.Errorf("窗口结果 ID 与验证不一致")
	}
	if err := w.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.readRequestLocked(id); err != nil {
		return err
	}
	if s.reportExistsLocked(id) {
		return errPortfolioValidationCompleted
	}
	dir := s.windowsDir(id)
	if !withinRoot(s.root, dir) {
		return fmt.Errorf("验证 ID 派生路径越界")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, fmt.Sprintf("%04d.json", w.Index)), buf)
}

// Complete 汇总并发布最终结论：读取冻结 spec + 全部窗口结果，由后端
// Evaluate 生成 verdict（passed | failed | insufficient | error），原子写
// 报告（完成标记，只允许一次）。客户端不能指定 verdict。
func (s *PortfolioValidationStore) Complete(id string) (PortfolioValidationReport, error) {
	if !validPortfolioValidationID(id) {
		return PortfolioValidationReport{}, fmt.Errorf("非法验证 ID: %q", id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.readRequestLocked(id)
	if err != nil {
		return PortfolioValidationReport{}, err
	}
	if s.reportExistsLocked(id) {
		return PortfolioValidationReport{}, errPortfolioValidationCompleted
	}
	windows, err := s.readWindowsLocked(id)
	if err != nil {
		return PortfolioValidationReport{}, err
	}
	if len(windows) == 0 {
		return PortfolioValidationReport{}, fmt.Errorf("没有窗口结果，无法生成结论（至少需保存一个窗口）")
	}
	res, err := rec.Spec.Evaluate(windows)
	if err != nil {
		return PortfolioValidationReport{}, fmt.Errorf("门禁求值失败: %w", err)
	}
	report := PortfolioValidationReport{
		SchemaVersion:    portfolioValidationSchemaVersion,
		ValidationID:     id,
		FinishedAt:       s.now().UTC().Format(time.RFC3339),
		Verdict:          res.Verdict,
		EvidenceClass:    res.EvidenceClass,
		Gates:            res.Gates,
		WindowCount:      res.WindowCount,
		ValidWindowCount: res.ValidWindowCount,
		Message:          res.Message,
	}
	if err := report.validate(); err != nil {
		return PortfolioValidationReport{}, err
	}
	buf, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return PortfolioValidationReport{}, err
	}
	if err := atomicWrite(s.reportPath(id), buf); err != nil {
		return PortfolioValidationReport{}, err
	}
	return report, nil
}

// readWindowsLocked 读取全部窗口结果，按 Index 升序。文件名 NNNN 必须与
// 内容 Index 一致，窗口结果 ID 与验证一致，损坏/状态非法 fail closed。
func (s *PortfolioValidationStore) readWindowsLocked(id string) ([]portfolioresearch.ValidationWindowOutcome, error) {
	dir := s.windowsDir(id)
	if !withinRoot(s.root, dir) {
		return nil, fmt.Errorf("验证 ID 派生路径越界")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []portfolioresearch.ValidationWindowOutcome
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".json")
		fileIndex, err := strconv.Atoi(base)
		if err != nil || fileIndex < 1 {
			continue // .tmp-* 等非正式文件忽略
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var w portfolioresearch.ValidationWindowOutcome
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, fmt.Errorf("窗口结果损坏: %w", err)
		}
		if w.Index != fileIndex {
			return nil, fmt.Errorf("窗口序号与文件名不一致: %d ≠ %s", w.Index, e.Name())
		}
		if w.ValidationID != id {
			return nil, fmt.Errorf("窗口结果 ID 与目录不一致")
		}
		if err := w.Validate(); err != nil {
			return nil, fmt.Errorf("窗口 %d 结果非法: %w", w.Index, err)
		}
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out, nil
}

// readReportLocked 读取最终报告（存在时）；基本合法性由报告自身 validate
// 校验，ID 与目录一致性在此校验。文件不存在返回 (nil, nil)。
func (s *PortfolioValidationStore) readReportLocked(id string) (*PortfolioValidationReport, error) {
	data, err := os.ReadFile(s.reportPath(id))
	switch {
	case os.IsNotExist(err):
		return nil, nil
	case err != nil:
		return nil, err
	}
	var rep PortfolioValidationReport
	if err := json.Unmarshal(data, &rep); err != nil {
		return nil, fmt.Errorf("组合验证报告损坏: %w", err)
	}
	if err := rep.validate(); err != nil {
		return nil, err
	}
	if rep.ValidationID != id {
		return nil, fmt.Errorf("验证报告 ID 与目录不一致")
	}
	return &rep, nil
}

// reportExistsLocked 完成标记探测（调用方已持锁）。
func (s *PortfolioValidationStore) reportExistsLocked(id string) bool {
	_, err := os.Stat(s.reportPath(id))
	return err == nil
}

// supersededByLocked 扫描全部冻结主记录，返回替换指定验证的 ID 列表（升序）。
func (s *PortfolioValidationStore) supersededByLocked(id string) ([]string, error) {
	ids, err := s.validationDirs()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, other := range ids {
		if other == id {
			continue
		}
		rec, err := s.readRequestLocked(other)
		if err != nil {
			return nil, err
		}
		if rec.Supersedes == id {
			out = append(out, other)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Get 返回验证详情：冻结记录（spec hash 重算校验）、派生状态（报告存在
// 即 completed）、窗口结果与最终报告。completed 记录重算 verdict + 证据
// 等级与存储报告一致，不匹配 fail closed（最终结论由冻结 spec 唯一决定）。
func (s *PortfolioValidationStore) Get(id string) (PortfolioValidationView, error) {
	if !validPortfolioValidationID(id) {
		return PortfolioValidationView{}, fmt.Errorf("非法验证 ID: %q", id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.readRequestLocked(id)
	if err != nil {
		return PortfolioValidationView{}, err
	}
	windows, err := s.readWindowsLocked(id)
	if err != nil {
		return PortfolioValidationView{}, err
	}
	report, err := s.readReportLocked(id)
	if err != nil {
		return PortfolioValidationView{}, err
	}
	state := PortfolioValidationStateCreated
	if report != nil {
		state = PortfolioValidationStateCompleted
		// 读取重算最终结论：verdict + 证据等级必须与存储一致（fail closed）。
		res, err := rec.Spec.Evaluate(windows)
		if err != nil {
			return PortfolioValidationView{}, fmt.Errorf("重算验证结论失败: %w", err)
		}
		if res.Verdict != report.Verdict || res.EvidenceClass != report.EvidenceClass {
			return PortfolioValidationView{}, fmt.Errorf("验证结论重算不一致（记录不可信，fail closed）")
		}
	}
	supersededBy, err := s.supersededByLocked(id)
	if err != nil {
		return PortfolioValidationView{}, err
	}
	return PortfolioValidationView{
		Record:       rec,
		State:        state,
		Windows:      windows,
		Report:       report,
		SupersededBy: supersededBy,
	}, nil
}

// List 返回验证摘要分页列表。筛选：模型、结论（仅 completed）、证据等级。
// 排序：CreatedAt 倒序，ID 升序兜底稳定。损坏记录 fail closed。
func (s *PortfolioValidationStore) List(filter PortfolioValidationFilter, page, pageSize int) (PortfolioValidationPage, error) {
	if err := filter.validate(); err != nil {
		return PortfolioValidationPage{}, err
	}
	if page < 1 {
		return PortfolioValidationPage{}, fmt.Errorf("page 无效: %d（应为 >=1）", page)
	}
	if pageSize < 1 || pageSize > MaxPortfolioValidationPageSize {
		return PortfolioValidationPage{}, fmt.Errorf("pageSize 无效: %d（应为 1-%d）", pageSize, MaxPortfolioValidationPageSize)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	ids, err := s.validationDirs()
	if err != nil {
		return PortfolioValidationPage{}, err
	}
	var items []PortfolioValidationSummary
	for _, id := range ids {
		rec, err := s.readRequestLocked(id)
		if err != nil {
			if errors.Is(err, errPortfolioValidationNotFound) {
				continue // 目录未发布主记录（理论上不该出现），忽略
			}
			return PortfolioValidationPage{}, err // 损坏 fail closed
		}
		summary := PortfolioValidationSummary{
			ID:            rec.ID,
			CreatedAt:     rec.CreatedAt,
			State:         PortfolioValidationStateCreated,
			EvidenceClass: rec.ModelEvidenceClass,
			ModelID:       rec.Spec.ModelRef.ModelID,
			ModelRevision: rec.Spec.ModelRef.Revision,
			ModelHash:     rec.Spec.ModelRef.Hash,
			WindowRule:    rec.Spec.WindowRule,
			Supersedes:    rec.Supersedes,
		}
		if s.reportExistsLocked(rec.ID) {
			rep, err := s.readReportLocked(rec.ID)
			if err != nil {
				return PortfolioValidationPage{}, err
			}
			summary.State = PortfolioValidationStateCompleted
			summary.Verdict = rep.Verdict
			summary.EvidenceClass = rep.EvidenceClass
		}
		if filter.ModelID != "" && rec.Spec.ModelRef.ModelID != filter.ModelID {
			continue
		}
		if filter.Verdict != "" && summary.Verdict != filter.Verdict {
			continue
		}
		if filter.EvidenceClass != "" && summary.EvidenceClass != filter.EvidenceClass {
			continue
		}
		items = append(items, summary)
	}
	// SupersededBy 由全部记录的 Supersedes 派生。
	for i := range items {
		for _, other := range items {
			if other.Supersedes == items[i].ID {
				items[i].SupersededBy = append(items[i].SupersededBy, other.ID)
			}
		}
		sort.Strings(items[i].SupersededBy)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt != items[j].CreatedAt {
			return items[i].CreatedAt > items[j].CreatedAt // 倒序：最新在前
		}
		return items[i].ID < items[j].ID // ID 升序兜底稳定
	})
	total := len(items)
	start := (page - 1) * pageSize
	if start >= total {
		return PortfolioValidationPage{Items: []PortfolioValidationSummary{}, Total: total, Page: page, PageSize: pageSize}, nil
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	return PortfolioValidationPage{Items: items[start:end], Total: total, Page: page, PageSize: pageSize}, nil
}

// findByRequestIDLocked 按幂等键扫描全部主记录（忽略目录与 .tmp-* 残留）。
func (s *PortfolioValidationStore) findByRequestIDLocked(requestID string) (*PortfolioValidationRecord, error) {
	ids, err := s.validationDirs()
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		rec, err := s.readRequestLocked(id)
		if err != nil {
			if errors.Is(err, errPortfolioValidationNotFound) {
				continue
			}
			return nil, err // 损坏 fail closed
		}
		if rec.RequestID == requestID {
			r := rec
			return &r, nil
		}
	}
	return nil, nil
}
