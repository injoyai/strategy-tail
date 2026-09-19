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
)

// factor_validation_store.go 冻结验证不可变存储（设计 §12.1，计划 Task 7
// Step 4）。目录布局（root 生产默认 data/lab/factor-validations）：
//
//	<root>/<validationId>/
//	  request.json        # 冻结请求：Create 先发布，之后永不修改
//	  report.json         # 最终报告：Complete 一次写入，完成标记
//	  windows/NNNN.json   # 窗口进度：完成前可重写，完成后拒绝修改
//
// 所有正式文件 tmp+rename 原子写；读取时重算冻结 hash，不匹配 fail closed；
// API 不接收路径，错误不暴露绝对路径。

// validationDataDir 验证库生产默认根（data/ 为本地持久数据，不进 Git）。
const validationDataDir = "data/lab/factor-validations"

// 验证库错误（API 层映射状态码）。
var (
	errValidationNotFound  = errors.New("验证不存在")
	errValidationCompleted = errors.New("验证已完成，拒绝修改（不可变记录）")
)

// DefaultValidationRoot 生产默认验证目录。
func DefaultValidationRoot() string { return filepath.Join("data", "lab", "factor-validations") }

// FreezeDependencies 冻结所需依赖：候选按 revision 读取、家族 trial 账本、
// 发现期不可变分析报告。
type FreezeDependencies struct {
	Candidates *CandidateStore
	Trials     *TrialStore
	Analyses   *AnalysisStore
}

// ValidationStore 冻结验证追加式存储。
type ValidationStore struct {
	root string
	mu   sync.Mutex
}

// NewValidationStore 创建验证存储；测试必须使用 t.TempDir()。
func NewValidationStore(root string) *ValidationStore { return &ValidationStore{root: root} }

func (s *ValidationStore) requestPath(id string) string {
	return filepath.Join(s.root, id, "request.json")
}

func (s *ValidationStore) reportPath(id string) string {
	return filepath.Join(s.root, id, "report.json")
}

func (s *ValidationStore) windowsDir(id string) string {
	return filepath.Join(s.root, id, "windows")
}

// validationDirs 扫描根目录下全部合法验证目录（不排序）。
func (s *ValidationStore) validationDirs() ([]string, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() && validValidationID(e.Name()) {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}

// readRequest 读取并完整校验冻结请求：schema、ID 与目录一致、冻结 hash
// 重算一致、协议合法、研究协议 hash 一致、候选引用一致。损坏 fail closed。
func (s *ValidationStore) readRequest(id string) (ValidationRequest, error) {
	path := s.requestPath(id)
	if !withinRoot(s.root, path) {
		return ValidationRequest{}, fmt.Errorf("验证 ID 派生路径越界")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ValidationRequest{}, errValidationNotFound
		}
		return ValidationRequest{}, err
	}
	var vr ValidationRequest
	if err := json.Unmarshal(data, &vr); err != nil {
		return ValidationRequest{}, fmt.Errorf("验证请求损坏: %w", err)
	}
	if vr.SchemaVersion != validationSchemaVersion {
		return ValidationRequest{}, fmt.Errorf("未知验证请求 schema 版本: %d", vr.SchemaVersion)
	}
	if vr.ID != id {
		return ValidationRequest{}, fmt.Errorf("验证请求 ID 与目录不一致")
	}
	hash, err := validationRequestHash(vr)
	if err != nil {
		return ValidationRequest{}, err
	}
	if hash != vr.RequestHash {
		return ValidationRequest{}, fmt.Errorf("验证冻结哈希不匹配（记录不可信）")
	}
	if err := vr.Protocol.Validate(); err != nil {
		return ValidationRequest{}, fmt.Errorf("验证协议非法: %w", err)
	}
	if vr.ResearchProtocol != nil {
		ph, err := protocolHash(*vr.ResearchProtocol)
		if err != nil {
			return ValidationRequest{}, err
		}
		if ph != vr.ResearchProtocolHash {
			return ValidationRequest{}, fmt.Errorf("研究协议哈希不匹配（记录不可信）")
		}
	}
	if vr.Candidate.ID != vr.Protocol.CandidateID || vr.Candidate.Revision != vr.Protocol.CandidateRevision {
		return ValidationRequest{}, fmt.Errorf("候选引用与验证协议不一致")
	}
	return vr, nil
}

// Create 从指定候选 revision 冻结并创建验证。RequestID 幂等：同 ID 同请求
// hash 返回已有记录（created=false）；同 ID 不同 hash 返回冲突。候选
// revision、发现期证据、家族 trial 清单由依赖存储加载并经 freezeValidation
// 八项校验；request.json 先发布且永不修改。
func (s *ValidationStore) Create(req CreateValidationRequest, deps FreezeDependencies) (ValidationRequest, bool, error) {
	norm, err := normalizeCreateValidationRequest(req)
	if err != nil {
		return ValidationRequest{}, false, err
	}
	reqHash, err := createValidationRequestHash(norm)
	if err != nil {
		return ValidationRequest{}, false, err
	}
	if deps.Candidates == nil || deps.Trials == nil || deps.Analyses == nil {
		return ValidationRequest{}, false, fmt.Errorf("冻结依赖缺失（候选/试验/分析存储均不可为空）")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 幂等扫描：RequestID 命中且请求 hash 一致 → 返回已有记录。
	ids, err := s.validationDirs()
	if err != nil {
		return ValidationRequest{}, false, err
	}
	for _, id := range ids {
		vr, err := s.readRequest(id)
		if err != nil {
			if errors.Is(err, errValidationNotFound) {
				continue // 目录未发布请求（理论上不该出现），忽略
			}
			return ValidationRequest{}, false, err // 损坏 fail closed
		}
		if vr.CreateRequestID == norm.RequestID {
			if vr.CreateRequestHash == reqHash {
				return vr, false, nil
			}
			return ValidationRequest{}, false, errIdempotencyConflict
		}
	}

	candidate, err := deps.Candidates.GetRevision(norm.CandidateID, norm.CandidateRevision)
	if err != nil {
		return ValidationRequest{}, false, err
	}
	evidence, err := deps.Analyses.Get(candidate.Evidence.AnalysisID)
	if err != nil {
		return ValidationRequest{}, false, fmt.Errorf("发现期证据读取失败: %w", err)
	}
	if evidence == nil {
		return ValidationRequest{}, false, fmt.Errorf("发现期证据缺失: %s", candidate.Evidence.AnalysisID)
	}
	trials, err := deps.Trials.ListFamily(evidence.Protocol.Trial.FamilyID)
	if err != nil {
		return ValidationRequest{}, false, err
	}

	now := time.Now()
	vr, err := freezeValidation(candidate, *evidence, trials, norm, now)
	if err != nil {
		return ValidationRequest{}, false, err
	}
	id, err := newValidationID(now, nil)
	if err != nil {
		return ValidationRequest{}, false, fmt.Errorf("生成验证 ID 失败: %w", err)
	}
	vr.ID = id
	hash, err := validationRequestHash(vr)
	if err != nil {
		return ValidationRequest{}, false, err
	}
	vr.RequestHash = hash

	dir := filepath.Join(s.root, id)
	if !withinRoot(s.root, dir) {
		return ValidationRequest{}, false, fmt.Errorf("验证 ID 派生路径越界")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return ValidationRequest{}, false, err
	}
	buf, err := json.MarshalIndent(vr, "", "  ")
	if err != nil {
		return ValidationRequest{}, false, err
	}
	if err := atomicWrite(s.requestPath(id), buf); err != nil {
		return ValidationRequest{}, false, err
	}
	return vr, true, nil
}

// ValidationView 验证详情：冻结请求 + 派生状态 + 窗口进度 + 最终报告。
type ValidationView struct {
	Request ValidationRequest        `json:"request"`
	State   ValidationState          `json:"state"`
	Windows []ValidationWindowReport `json:"windows,omitempty"`
	Report  *FactorValidationReport  `json:"report,omitempty"`
	// SupersededBy 替换本记录的验证 ID 列表（由新记录的 Supersedes 派生，
	// 不改写本记录——request.json 永不修改）。
	SupersededBy []string `json:"supersededBy,omitempty"`
}

// readReport 读取最终报告（存在时）；基本合法性由报告自身 validate 校验，
// ID 与目录一致性在此校验。文件不存在返回 (nil, nil)。
func (s *ValidationStore) readReport(id string) (*FactorValidationReport, error) {
	data, err := os.ReadFile(s.reportPath(id))
	switch {
	case os.IsNotExist(err):
		return nil, nil
	case err != nil:
		return nil, err
	}
	var rep FactorValidationReport
	if err := json.Unmarshal(data, &rep); err != nil {
		return nil, fmt.Errorf("验证报告损坏: %w", err)
	}
	if err := rep.validate(); err != nil {
		return nil, err
	}
	if rep.ValidationID != id {
		return nil, fmt.Errorf("验证报告 ID 与目录不一致")
	}
	return &rep, nil
}

// readWindows 读取全部窗口进度报告，按 Index 升序。文件名 NNNN 必须与
// 内容 Index 一致，损坏或状态非法 fail closed。
func (s *ValidationStore) readWindows(id string) ([]ValidationWindowReport, error) {
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
	var out []ValidationWindowReport
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
		var w ValidationWindowReport
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, fmt.Errorf("窗口报告损坏: %w", err)
		}
		if w.SchemaVersion != validationSchemaVersion {
			return nil, fmt.Errorf("未知窗口报告 schema 版本: %d", w.SchemaVersion)
		}
		if w.ValidationID != id {
			return nil, fmt.Errorf("窗口报告 ID 与目录不一致")
		}
		if w.Index != fileIndex {
			return nil, fmt.Errorf("窗口报告序号与文件名不一致: %d ≠ %s", w.Index, e.Name())
		}
		switch w.State {
		case ValidationWindowOK, ValidationWindowInsufficient, ValidationWindowError:
		default:
			return nil, fmt.Errorf("窗口 %d 状态非法: %q", w.Index, w.State)
		}
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out, nil
}

// supersededBy 扫描全部冻结请求，返回替换指定验证的 ID 列表（升序）。
func (s *ValidationStore) supersededBy(id string) ([]string, error) {
	ids, err := s.validationDirs()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, other := range ids {
		if other == id {
			continue
		}
		vr, err := s.readRequest(other)
		if err != nil {
			return nil, err
		}
		if vr.Supersedes == id {
			out = append(out, other)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Get 返回验证详情：冻结请求（hash 重算校验）、派生状态（报告存在即终态，
// 否则 frozen；running 属于 Runner 内存进度）、窗口进度与最终报告。
func (s *ValidationStore) Get(id string) (ValidationView, error) {
	if !validValidationID(id) {
		return ValidationView{}, fmt.Errorf("非法验证 ID: %q", id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	vr, err := s.readRequest(id)
	if err != nil {
		return ValidationView{}, err
	}
	windows, err := s.readWindows(id)
	if err != nil {
		return ValidationView{}, err
	}
	report, err := s.readReport(id)
	if err != nil {
		return ValidationView{}, err
	}
	state := ValidationStateFrozen
	if report != nil {
		state = report.State
	}
	supersededBy, err := s.supersededBy(id)
	if err != nil {
		return ValidationView{}, err
	}
	return ValidationView{
		Request:      vr,
		State:        state,
		Windows:      windows,
		Report:       report,
		SupersededBy: supersededBy,
	}, nil
}

// SaveWindow 写入单个窗口进度报告。验证必须存在且未完成（report.json 是
// 完成标记）；完成前允许重写同一窗口（崩溃恢复重跑属进度语义），完成后
// 一律拒绝。Index 1~9999 且与文件名一致。
func (s *ValidationStore) SaveWindow(id string, window ValidationWindowReport) error {
	if !validValidationID(id) {
		return fmt.Errorf("非法验证 ID: %q", id)
	}
	if window.Index < 1 || window.Index > 9999 {
		return fmt.Errorf("窗口序号无效: %d（应为 1-9999）", window.Index)
	}
	if window.ValidationID != id {
		return fmt.Errorf("窗口报告 ID 与验证不一致")
	}
	if window.SchemaVersion != validationSchemaVersion {
		return fmt.Errorf("未知窗口报告 schema 版本: %d", window.SchemaVersion)
	}
	switch window.State {
	case ValidationWindowOK, ValidationWindowInsufficient, ValidationWindowError:
	default:
		return fmt.Errorf("窗口 %d 状态非法: %q", window.Index, window.State)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.readRequest(id); err != nil {
		return err
	}
	if _, err := s.readReport(id); err != nil {
		return err
	} else if s.reportExistsLocked(id) {
		return errValidationCompleted
	}
	dir := s.windowsDir(id)
	if !withinRoot(s.root, dir) {
		return fmt.Errorf("验证 ID 派生路径越界")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(window, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, fmt.Sprintf("%04d.json", window.Index)), buf)
}

// reportExistsLocked 完成标记探测（调用方已持锁）。
func (s *ValidationStore) reportExistsLocked(id string) bool {
	_, err := os.Stat(s.reportPath(id))
	return err == nil
}

// Complete 写入最终不可变报告（完成标记）。请求必须已发布且报告尚未写入
// （追加式一次写入）；报告必须为终态且 Verdict 与 State 一致。
func (s *ValidationStore) Complete(id string, report FactorValidationReport) error {
	if !validValidationID(id) {
		return fmt.Errorf("非法验证 ID: %q", id)
	}
	if report.ValidationID != id {
		return fmt.Errorf("验证报告 ID 与验证不一致")
	}
	if err := report.validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.readRequest(id); err != nil {
		return err
	}
	if _, err := os.Stat(s.reportPath(id)); err == nil {
		return errValidationCompleted
	} else if !os.IsNotExist(err) {
		return err
	}
	buf, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(s.reportPath(id), buf)
}

// ValidationFilter 列表筛选（全部字段可选；State/EvidenceClass 使用白名单
// 枚举，未知值拒绝）。
type ValidationFilter struct {
	CandidateID   string
	State         ValidationState
	EvidenceClass string
}

func (f ValidationFilter) validate() error {
	if f.CandidateID != "" && !validCandidateID(f.CandidateID) {
		return fmt.Errorf("非法候选 ID: %q", f.CandidateID)
	}
	if f.State != "" && f.State != ValidationStateFrozen && !finishedValidationStates[f.State] {
		return fmt.Errorf("筛选状态非法: %q", f.State)
	}
	switch f.EvidenceClass {
	case "", validationEvidenceRetrospective, validationEvidenceProspective:
	default:
		return fmt.Errorf("筛选证据等级非法: %q", f.EvidenceClass)
	}
	return nil
}

// ValidationSummary 验证列表摘要（不含窗口与报告详情）。
type ValidationSummary struct {
	ID                string          `json:"id"`
	FrozenAt          string          `json:"frozenAt"`
	State             ValidationState `json:"state"`
	EvidenceClass     string          `json:"evidenceClass"`
	CandidateID       string          `json:"candidateId"`
	CandidateRevision int             `json:"candidateRevision"`
	Factor            FactorRef       `json:"factor"`
	Windows           WalkForwardSpec `json:"windows"`
	TrialCount        int             `json:"trialCount"`
	Supersedes        string          `json:"supersedes,omitempty"`
	// SupersededBy 替换本记录的验证 ID 列表（升序）。
	SupersededBy []string `json:"supersededBy,omitempty"`
}

// List 返回验证摘要列表。筛选：候选、状态（含 frozen）、证据等级。
// 排序：FrozenAt 倒序（最新在前），ID 升序兜底稳定。损坏记录 fail closed。
func (s *ValidationStore) List(filter ValidationFilter) ([]ValidationSummary, error) {
	if err := filter.validate(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	ids, err := s.validationDirs()
	if err != nil {
		return nil, err
	}
	type loaded struct {
		vr      ValidationRequest
		state   ValidationState
		summary ValidationSummary
	}
	var items []loaded
	for _, id := range ids {
		vr, err := s.readRequest(id)
		if err != nil {
			if errors.Is(err, errValidationNotFound) {
				continue
			}
			return nil, err
		}
		state := ValidationStateFrozen
		if s.reportExistsLocked(id) {
			rep, err := s.readReport(id)
			if err != nil {
				return nil, err
			}
			state = rep.State
		}
		if filter.CandidateID != "" && vr.Protocol.CandidateID != filter.CandidateID {
			continue
		}
		if filter.State != "" && state != filter.State {
			continue
		}
		if filter.EvidenceClass != "" && vr.Protocol.EvidenceClass != filter.EvidenceClass {
			continue
		}
		items = append(items, loaded{
			vr:    vr,
			state: state,
			summary: ValidationSummary{
				ID:                vr.ID,
				FrozenAt:          vr.FrozenAt,
				State:             state,
				EvidenceClass:     vr.Protocol.EvidenceClass,
				CandidateID:       vr.Protocol.CandidateID,
				CandidateRevision: vr.Protocol.CandidateRevision,
				Factor:            vr.Candidate.Factor,
				Windows:           vr.Protocol.Windows,
				TrialCount:        len(vr.Trials),
				Supersedes:        vr.Supersedes,
			},
		})
	}
	// SupersededBy 由全部记录的 Supersedes 派生。
	for i := range items {
		for _, other := range items {
			if other.vr.Supersedes == items[i].vr.ID {
				items[i].summary.SupersededBy = append(items[i].summary.SupersededBy, other.vr.ID)
			}
		}
		sort.Strings(items[i].summary.SupersededBy)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].vr.FrozenAt != items[j].vr.FrozenAt {
			return items[i].vr.FrozenAt > items[j].vr.FrozenAt
		}
		return items[i].vr.ID < items[j].vr.ID
	})
	out := make([]ValidationSummary, 0, len(items))
	for _, it := range items {
		out = append(out, it.summary)
	}
	return out, nil
}
