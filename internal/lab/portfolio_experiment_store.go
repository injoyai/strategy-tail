package lab

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/injoyai/strategy-tail/internal/portfolioresearch"
)

// portfolio_experiment_store.go v2 Task 7 实验账本存储与产物发布协议
// （设计 §12.1/§12.2/§14）。
//
// 目录布局（recordsRoot 默认 data/lab/portfolio-experiments，
// artifactsRoot 默认 output/portfolio）：
//
//	<recordsRoot>/<experiment-id>.json           # 主记录（tmp+rename 原子发布）
//	<artifactsRoot>/<experiment-id>/report.json  # 五类正式产物（Runner 写入）
//	<artifactsRoot>/<experiment-id>/nav.csv
//	<artifactsRoot>/<experiment-id>/orders.csv
//	<artifactsRoot>/<experiment-id>/trades.csv
//	<artifactsRoot>/<experiment-id>/holdings.csv
//
// 发布协议：MarkCompleted 先扫描产物目录生成 manifest（文件名→SHA-256）与
// 报告 hash，再原子写主记录（状态=completed 且含 manifest）。产物缺失、
// 五类不全或 hash 不一致 → 发布失败（error），主记录不呈现 completed。
// 读取 completed 记录时重算磁盘产物 hash 与清单比对，不匹配 fail closed；
// 崩溃残留的 .tmp-* 文件被忽略，不污染正式记录。
//
// 列表采用服务端分页与稳定排序（CreatedAt 倒序 + ID 升序兜底）；Store 只
// 暴露安全相对产物名，不返回本机绝对路径。

const (
	// DefaultExperimentPageSize 列表默认每页条数。
	DefaultExperimentPageSize = 20
	// MaxExperimentPageSize 列表每页条数上限（资源保护）。
	MaxExperimentPageSize = 200
)

// DefaultPortfolioExperimentRoot 生产默认实验账本目录。
func DefaultPortfolioExperimentRoot() string {
	return filepath.Join("data", "lab", "portfolio-experiments")
}

// DefaultPortfolioArtifactRoot 生产默认产物目录。
func DefaultPortfolioArtifactRoot() string {
	return filepath.Join("output", "portfolio")
}

// PortfolioExperimentStore 实验账本存储。Create 幂等（同 RequestID 同请求
// hash 返回已有记录，不同 hash 冲突）；并发写经互斥锁串行化，状态迁移
// 一次生效；主记录原子发布，绝不存在"completed 但产物不完整"的可见状态。
type PortfolioExperimentStore struct {
	recordsRoot   string
	artifactsRoot string
	mu            sync.Mutex
	now           func() time.Time
	rand          io.Reader
}

// NewPortfolioExperimentStore 创建实验存储；测试必须使用 t.TempDir()。
func NewPortfolioExperimentStore(recordsRoot, artifactsRoot string) *PortfolioExperimentStore {
	return &PortfolioExperimentStore{
		recordsRoot:   recordsRoot,
		artifactsRoot: artifactsRoot,
		now:           time.Now,
		rand:          rand.Reader,
	}
}

func (s *PortfolioExperimentStore) recordPath(id string) string {
	return filepath.Join(s.recordsRoot, id+".json")
}

func (s *PortfolioExperimentStore) artifactPath(id, name string) string {
	return filepath.Join(s.artifactsRoot, id, name)
}

// Create 创建实验（初始状态 queued，服务端生成 pe_* ID）。失败、取消与
// 无有效样本的运行同样进入账本——登记元数据不依赖最终结果。
func (s *PortfolioExperimentStore) Create(req CreatePortfolioExperimentRequest) (PortfolioExperiment, bool, error) {
	norm, err := normalizeCreatePortfolioExperimentRequest(req)
	if err != nil {
		return PortfolioExperiment{}, false, err
	}
	reqHash, err := createPortfolioExperimentRequestHash(norm)
	if err != nil {
		return PortfolioExperiment{}, false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 幂等扫描：RequestID 命中且请求 hash 一致 → 返回已有记录。
	existing, err := s.findByRequestIDLocked(norm.RequestID)
	if err != nil {
		return PortfolioExperiment{}, false, err
	}
	if existing != nil {
		if existing.RequestHash == reqHash {
			return *existing, false, nil
		}
		return PortfolioExperiment{}, false, errIdempotencyConflict
	}

	id, err := newExperimentID(s.now(), s.rand)
	if err != nil {
		return PortfolioExperiment{}, false, fmt.Errorf("生成实验 ID 失败: %w", err)
	}
	rec := buildExperimentRecord(norm, id, reqHash, s.now())
	if err := s.writeRecordLocked(rec); err != nil {
		return PortfolioExperiment{}, false, err
	}
	return rec, true, nil
}

// Get 读取实验详情（含状态/元数据/产物清单）。completed 记录重算磁盘产物
// hash 与清单比对，不匹配 fail closed；损坏 JSON fail closed。
func (s *PortfolioExperimentStore) Get(id string) (PortfolioExperiment, error) {
	if !validExperimentID(id) {
		return PortfolioExperiment{}, fmt.Errorf("非法实验 ID: %q", id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readRecordLocked(id)
}

// Start 迁移 queued → running（Runner 接受任务时调用）。
func (s *PortfolioExperimentStore) Start(id string) error {
	if !validExperimentID(id) {
		return fmt.Errorf("非法实验 ID: %q", id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.saveLocked(id, func(rec *PortfolioExperiment) error {
		if err := experimentTransition(rec.Status, portfolioresearch.RunStateRunning); err != nil {
			return err
		}
		rec.Status = portfolioresearch.RunStateRunning
		return nil
	})
	return err
}

// UpdateProgress 更新运行进度（0-100 与中间说明）。仅 running 允许；终态
// 拒绝。note 为可选中间诊断，rune 数受限。
func (s *PortfolioExperimentStore) UpdateProgress(id string, progress int, note string) error {
	if !validExperimentID(id) {
		return fmt.Errorf("非法实验 ID: %q", id)
	}
	if progress < 0 || progress > 100 {
		return fmt.Errorf("进度无效: %d（应为 0-100）", progress)
	}
	if n := runeLen(note); n > DefaultExperimentLimits().MaxErrorRunes {
		return fmt.Errorf("进度说明过长: %d（最多 %d 个字符）", n, DefaultExperimentLimits().MaxErrorRunes)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.saveLocked(id, func(rec *PortfolioExperiment) error {
		if rec.Status != portfolioresearch.RunStateRunning {
			return fmt.Errorf("仅 running 允许更新进度，当前 %q", rec.Status)
		}
		rec.Progress = progress
		rec.Message = note
		return nil
	})
	return err
}

// MarkCompleted 原子发布完成：扫描产物目录生成 manifest（文件名→SHA-256）
// 与报告 hash，再原子写主记录（状态=completed 且含 manifest）。五类产物
// 缺失或读取失败 → 发布失败（error），主记录保持 running，不呈现 completed
// （完成门槛）。已终态记录拒绝（errExperimentFinalized）。
func (s *PortfolioExperimentStore) MarkCompleted(id string) error {
	if !validExperimentID(id) {
		return fmt.Errorf("非法实验 ID: %q", id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.saveLocked(id, func(rec *PortfolioExperiment) error {
		if err := experimentTransition(rec.Status, portfolioresearch.RunStateCompleted); err != nil {
			return err
		}
		manifest, reportHash, err := s.scanArtifactsLocked(id)
		if err != nil {
			// 产物不全或 hash 校验失败 → 发布失败，主记录不写 completed。
			return err
		}
		rec.Status = portfolioresearch.RunStateCompleted
		rec.Manifest = manifest
		rec.ReportHash = reportHash
		rec.ReportPath = "report.json"
		rec.Progress = 100
		rec.Error = ""
		return nil
	})
	return err
}

// MarkFailed 标记失败：保留全部元数据与诊断信息，不发布正式报告。
func (s *PortfolioExperimentStore) MarkFailed(id, message string) error {
	return s.markTerminal(id, portfolioresearch.RunStateFailed, message)
}

// MarkCancelled 标记取消（设计 §14）：保存 cancelled 与已有诊断，不发布
// 正式报告。
func (s *PortfolioExperimentStore) MarkCancelled(id, message string) error {
	return s.markTerminal(id, portfolioresearch.RunStateCancelled, message)
}

// MarkInsufficient 标记无有效样本：运行成功但样本不足，保留元数据。
func (s *PortfolioExperimentStore) MarkInsufficient(id, message string) error {
	return s.markTerminal(id, portfolioresearch.RunStateInsufficient, message)
}

// markTerminal 通用终态标记：running → 终态，保留元数据并写入诊断。
func (s *PortfolioExperimentStore) markTerminal(id, target, message string) error {
	if !validExperimentID(id) {
		return fmt.Errorf("非法实验 ID: %q", id)
	}
	if !finishedExperimentStates[target] {
		return fmt.Errorf("非法终态: %q", target)
	}
	if n := runeLen(message); n > DefaultExperimentLimits().MaxErrorRunes {
		return fmt.Errorf("诊断信息过长: %d（最多 %d 个字符）", n, DefaultExperimentLimits().MaxErrorRunes)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.saveLocked(id, func(rec *PortfolioExperiment) error {
		if err := experimentTransition(rec.Status, target); err != nil {
			return err
		}
		rec.Status = target
		rec.Error = message
		return nil
	})
	return err
}

// ExperimentFilter 列表筛选（全部可选；FamilyID/ModelID/Status 白名单校验）。
type ExperimentFilter struct {
	FamilyID string
	ModelID  string
	Status   string
}

func (f ExperimentFilter) validate() error {
	if f.FamilyID != "" && !protocolIDRe.MatchString(f.FamilyID) {
		return fmt.Errorf("筛选 familyId 非法: %q", f.FamilyID)
	}
	if f.ModelID != "" && !validModelID(f.ModelID) {
		return fmt.Errorf("筛选 modelId 非法: %q", f.ModelID)
	}
	if f.Status != "" && !portfolioresearch.ValidRunState(f.Status) {
		return fmt.Errorf("筛选 status 非法: %q", f.Status)
	}
	return nil
}

// ExperimentPage 服务端分页结果：稳定排序（CreatedAt 倒序 + ID 升序兜底），
// 页码可恢复（设计 §12.2）。
type ExperimentPage struct {
	Items    []PortfolioExperiment `json:"items"`
	Total    int                   `json:"total"`
	Page     int                   `json:"page"`
	PageSize int                   `json:"pageSize"`
}

// List 返回实验分页列表。page 从 1 起；pageSize 上限 MaxExperimentPageSize；
// page 越界返回空 items + 真实 total；损坏记录 fail closed。completed 记录
// 做完成门槛的结构校验（清单完整），产物 hash 深度校验由 Get 承担。
func (s *PortfolioExperimentStore) List(filter ExperimentFilter, page, pageSize int) (ExperimentPage, error) {
	if err := filter.validate(); err != nil {
		return ExperimentPage{}, err
	}
	if page < 1 {
		return ExperimentPage{}, fmt.Errorf("page 无效: %d（应为 >=1）", page)
	}
	if pageSize < 1 || pageSize > MaxExperimentPageSize {
		return ExperimentPage{}, fmt.Errorf("pageSize 无效: %d（应为 1-%d）", pageSize, MaxExperimentPageSize)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.recordsRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return ExperimentPage{Items: []PortfolioExperiment{}, Total: 0, Page: page, PageSize: pageSize}, nil
		}
		return ExperimentPage{}, err
	}
	var all []PortfolioExperiment
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".tmp-") {
			continue // 目录与崩溃残留的 tmp 文件忽略
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		if !validExperimentID(id) {
			continue
		}
		rec, err := s.readRecordRawLocked(id)
		if err != nil {
			if errors.Is(err, errExperimentNotFound) {
				continue
			}
			return ExperimentPage{}, err // 损坏 fail closed
		}
		if err := rec.validateCompleted(); err != nil {
			return ExperimentPage{}, err
		}
		if filter.FamilyID != "" && rec.FamilyID != filter.FamilyID {
			continue
		}
		if filter.ModelID != "" && rec.ModelID != filter.ModelID {
			continue
		}
		if filter.Status != "" && rec.Status != filter.Status {
			continue
		}
		all = append(all, rec)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].CreatedAt != all[j].CreatedAt {
			return all[i].CreatedAt > all[j].CreatedAt // 倒序：最新在前
		}
		return all[i].ExperimentID < all[j].ExperimentID // ID 升序兜底稳定
	})
	total := len(all)
	start := (page - 1) * pageSize
	if start >= total {
		return ExperimentPage{Items: []PortfolioExperiment{}, Total: total, Page: page, PageSize: pageSize}, nil
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	return ExperimentPage{Items: all[start:end], Total: total, Page: page, PageSize: pageSize}, nil
}

// ArtifactPaths 返回已发布产物的安全相对名列表（仅 completed 记录；按白名单
// 稳定顺序），不返回本机绝对路径。非 completed 返回空列表。manifest 中出现
// 非法产物名 fail closed。
func (s *PortfolioExperimentStore) ArtifactPaths(id string) ([]string, error) {
	if !validExperimentID(id) {
		return nil, fmt.Errorf("非法实验 ID: %q", id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.readRecordLocked(id)
	if err != nil {
		return nil, err
	}
	if rec.Status != portfolioresearch.RunStateCompleted || rec.Manifest == nil {
		return []string{}, nil
	}
	names := make([]string, 0, len(portfolioArtifactNames))
	for _, allowed := range portfolioArtifactNames {
		if rec.Manifest.hashOf(allowed) != "" {
			names = append(names, allowed)
		}
	}
	return names, nil
}

// ---- 内部实现 ----

// saveLocked 读改写通用入口（调用方已持锁）：读取原始记录 → 迁移/变更
// 回调 → 刷新 UpdatedAt → 原子写回。变更回调返回错误则放弃写入。
func (s *PortfolioExperimentStore) saveLocked(id string, mutate func(*PortfolioExperiment) error) (PortfolioExperiment, error) {
	rec, err := s.readRecordRawLocked(id)
	if err != nil {
		return PortfolioExperiment{}, err
	}
	if err := mutate(&rec); err != nil {
		return PortfolioExperiment{}, err
	}
	rec.UpdatedAt = s.now().UTC().Format(time.RFC3339)
	if err := s.writeRecordLocked(rec); err != nil {
		return PortfolioExperiment{}, err
	}
	return rec, nil
}

// readRecordLocked 深度读取：raw 校验 + completed 完成门槛校验 + 磁盘产物
// hash 重算比对（fail closed）。
func (s *PortfolioExperimentStore) readRecordLocked(id string) (PortfolioExperiment, error) {
	rec, err := s.readRecordRawLocked(id)
	if err != nil {
		return PortfolioExperiment{}, err
	}
	if err := rec.validateCompleted(); err != nil {
		return PortfolioExperiment{}, err
	}
	if rec.Status == portfolioresearch.RunStateCompleted {
		if err := s.verifyArtifactsLocked(id, rec.Manifest); err != nil {
			return PortfolioExperiment{}, err
		}
	}
	return rec, nil
}

// readRecordRawLocked 原始读取：JSON 解析、ID 与路径一致、状态/进度合法。
// 不访问产物目录，用于状态迁移前的读取（此时产物可能尚未发布）。
func (s *PortfolioExperimentStore) readRecordRawLocked(id string) (PortfolioExperiment, error) {
	if !validExperimentID(id) {
		return PortfolioExperiment{}, fmt.Errorf("非法实验 ID: %q", id)
	}
	path := s.recordPath(id)
	if !withinRoot(s.recordsRoot, path) {
		return PortfolioExperiment{}, fmt.Errorf("实验 ID 派生路径越界")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return PortfolioExperiment{}, errExperimentNotFound
		}
		return PortfolioExperiment{}, err
	}
	var rec PortfolioExperiment
	if err := json.Unmarshal(data, &rec); err != nil {
		return PortfolioExperiment{}, fmt.Errorf("实验记录损坏: %w", err)
	}
	if rec.ExperimentID != id {
		return PortfolioExperiment{}, fmt.Errorf("实验记录 ID 与文件名不一致")
	}
	if !portfolioresearch.ValidRunState(rec.Status) {
		return PortfolioExperiment{}, fmt.Errorf("实验状态非法: %q", rec.Status)
	}
	if rec.Progress < 0 || rec.Progress > 100 {
		return PortfolioExperiment{}, fmt.Errorf("实验进度非法: %d（应为 0-100）", rec.Progress)
	}
	return rec, nil
}

// verifyArtifactsLocked 重算磁盘产物 hash 与清单比对，不匹配 fail closed。
func (s *PortfolioExperimentStore) verifyArtifactsLocked(id string, m *ArtifactManifest) error {
	for _, e := range m.Entries {
		path := s.artifactPath(id, e.Name)
		if !withinRoot(s.artifactsRoot, path) {
			return fmt.Errorf("产物路径越界: %s", e.Name)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("产物 %s 读取失败: %w", e.Name, err)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != e.SHA256 {
			return fmt.Errorf("产物 %s 哈希不匹配（产物被篡改或损坏，fail closed）", e.Name)
		}
	}
	return nil
}

// scanArtifactsLocked 扫描产物目录：五类产物全部存在并按白名单顺序计算
// SHA-256 生成 manifest，返回 (manifest, reportHash)。缺一类即失败。
func (s *PortfolioExperimentStore) scanArtifactsLocked(id string) (*ArtifactManifest, string, error) {
	entries := make([]ArtifactEntry, 0, len(portfolioArtifactNames))
	for _, name := range portfolioArtifactNames {
		path := s.artifactPath(id, name)
		if !withinRoot(s.artifactsRoot, path) {
			return nil, "", fmt.Errorf("产物路径越界: %s", name)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, "", fmt.Errorf("产物缺失: %s（五类产物必须齐全才能发布完成）", name)
			}
			return nil, "", err
		}
		sum := sha256.Sum256(data)
		entries = append(entries, ArtifactEntry{Name: name, SHA256: hex.EncodeToString(sum[:])})
	}
	m := &ArtifactManifest{Entries: entries}
	return m, m.hashOf("report.json"), nil
}

// writeRecordLocked 原子写主记录（MkdirAll + tmp + rename）。
func (s *PortfolioExperimentStore) writeRecordLocked(rec PortfolioExperiment) error {
	if err := os.MkdirAll(s.recordsRoot, 0755); err != nil {
		return err
	}
	path := s.recordPath(rec.ExperimentID)
	if !withinRoot(s.recordsRoot, path) {
		return fmt.Errorf("实验 ID 派生路径越界")
	}
	buf, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, buf)
}

// findByRequestIDLocked 按幂等键扫描全部正式记录（忽略 .tmp-* 残留与目录）。
func (s *PortfolioExperimentStore) findByRequestIDLocked(requestID string) (*PortfolioExperiment, error) {
	entries, err := os.ReadDir(s.recordsRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".tmp-") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		if !validExperimentID(id) {
			continue
		}
		rec, err := s.readRecordRawLocked(id)
		if err != nil {
			if errors.Is(err, errExperimentNotFound) {
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
