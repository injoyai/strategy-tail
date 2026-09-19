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
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// factor_candidate_store.go 候选因子追加式本地存储（设计文档 §4.3）。
//
// 目录布局（root 生产默认 data/lab/factor-candidates）：
//
//	<root>/<candidateId>/revisions/NNNNNN.json
//	<root>/<candidateId>/revisions/NNNNNN.analysis.json
//
// 每次创建/编辑/归档都追加一个修订，不覆盖旧修订；每个修订绑定一份完整
// AnalysisReport 快照；*.analysis.json 先写、*.json 后写（记录文件是提交
// 标记）；所有文件原子写；读取时校验 SHA-256 与摘要一致性，损坏 fail closed。

// candidateIDRe 候选 ID 格式：fc_<UTC yyyyMMddTHHmmssSSSZ>_<8 hex>。
var candidateIDRe = regexp.MustCompile(`^fc_\d{8}T\d{9}Z_[0-9a-f]{8}$`)

// candidateDataDir 候选库生产默认根（data/ 为本地持久数据，不进 Git）。
const candidateDataDir = "data/lab/factor-candidates"

// 候选库错误（API 层映射状态码）。
var (
	errCandidateNotFound   = errors.New("候选因子不存在")
	errRevisionConflict    = errors.New("候选因子已被更新，请重新加载后再保存")
	errIdempotencyConflict = errors.New("幂等键被不同内容复用")
)

// newCandidateID 生成候选 ID。
func newCandidateID(now time.Time, random io.Reader) (string, error) {
	return newPrefixedID("fc", now, random)
}

// validCandidateID 严格校验候选 ID 格式。
func validCandidateID(id string) bool { return candidateIDRe.MatchString(id) }

// runeLen Unicode 字符数。
func runeLen(s string) int { return utf8.RuneCountInString(s) }

// CandidateStore 候选因子追加式存储。
type CandidateStore struct {
	root string
	mu   sync.Mutex
	now  func() time.Time
	rand io.Reader
}

// NewCandidateStore 创建候选存储。生产默认根 data/lab/factor-candidates；
// 测试必须使用 t.TempDir()。
func NewCandidateStore(root string) *CandidateStore {
	return &CandidateStore{
		root: root,
		now:  time.Now,
		rand: rand.Reader,
	}
}

// Create 创建候选：返回 bool 表示是否新建。
// 同一 requestId 与相同请求 hash 返回已有记录（幂等）；同一 requestId 与
// 不同 hash 返回冲突。不同 request ID 可从同一分析创建不同名称/用途候选。
func (s *CandidateStore) Create(req CreateCandidateRequest, rep *AnalysisReport) (FactorCandidate, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	norm, err := normalizeCreateCandidateRequest(req)
	if err != nil {
		return FactorCandidate{}, false, err
	}
	hash, err := candidateRequestHash(norm)
	if err != nil {
		return FactorCandidate{}, false, err
	}
	if existing, err := s.findByRequestID(norm.RequestID); err != nil {
		return FactorCandidate{}, false, err
	} else if existing != nil {
		if existing.CreateRequestHash == hash {
			return *existing, false, nil
		}
		return FactorCandidate{}, false, errIdempotencyConflict
	}

	c, err := candidateFromAnalysis(norm, rep, s.now())
	if err != nil {
		return FactorCandidate{}, false, err
	}
	c.CreateRequestID = norm.RequestID
	c.CreateRequestHash = hash
	id, err := newCandidateID(s.now(), s.rand)
	if err != nil {
		return FactorCandidate{}, false, err
	}
	c.ID = id
	c.Revision = 1
	repBuf, err := json.Marshal(rep)
	if err != nil {
		return FactorCandidate{}, false, err
	}
	if err := s.writeRevision(c, repBuf); err != nil {
		return FactorCandidate{}, false, err
	}
	return c, true, nil
}

// Get 返回指定候选的最新修订（含证据哈希与摘要一致性校验）。
func (s *CandidateStore) Get(id string) (FactorCandidate, error) {
	if !validCandidateID(id) {
		return FactorCandidate{}, fmt.Errorf("非法候选 ID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readLatest(id)
}

// GetRevision 返回指定候选的指定修订（计划 Task 7 Step 3：按 revision
// 安全读取）。路径由 ID/revision 派生，API 不接收文件名；复用 readRevision
// 的路径越界校验与证据 SHA-256/摘要一致性校验，损坏 fail closed。
func (s *CandidateStore) GetRevision(id string, revision int) (FactorCandidate, error) {
	if !validCandidateID(id) {
		return FactorCandidate{}, fmt.Errorf("非法候选 ID")
	}
	if revision < 1 {
		return FactorCandidate{}, fmt.Errorf("revision 无效: %d（应为 >=1）", revision)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, _, err := s.readRevision(id, revision)
	return c, err
}

// List 返回全部候选的最新修订。includeArchived=false 时隐藏归档候选。
// 排序：活动候选在前，同状态按 updatedAt 倒序，再按 ID 稳定排序。
func (s *CandidateStore) List(includeArchived bool) ([]FactorCandidate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []FactorCandidate
	for _, e := range entries {
		if !e.IsDir() || !validCandidateID(e.Name()) {
			continue
		}
		c, err := s.readLatest(e.Name())
		if err != nil {
			if errors.Is(err, errCandidateNotFound) {
				continue // 空目录/未提交完成，忽略
			}
			return nil, err // 损坏 fail closed
		}
		if !includeArchived && c.Status == CandidateStatusArchived {
			continue
		}
		out = append(out, c)
	}
	rank := func(st CandidateStatus) int {
		if st == CandidateStatusCandidate {
			return 0
		}
		return 1
	}
	sort.SliceStable(out, func(i, j int) bool {
		if rank(out[i].Status) != rank(out[j].Status) {
			return rank(out[i].Status) < rank(out[j].Status)
		}
		if out[i].UpdatedAt != out[j].UpdatedAt {
			return out[i].UpdatedAt > out[j].UpdatedAt
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Update 追加修订：必须匹配 expectedRevision（否则 409）；只允许名称、备注、
// Use、Status 变化；FactorRef/Evidence/CreatedAt/CreateRequestID/Hash 保持不变；
// candidate→archived→candidate 合法，其他状态拒绝。
func (s *CandidateStore) Update(id string, req UpdateCandidateRequest) (FactorCandidate, error) {
	if !validCandidateID(id) {
		return FactorCandidate{}, fmt.Errorf("非法候选 ID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	cur, evidence, err := s.readLatestFull(id)
	if err != nil {
		return FactorCandidate{}, err
	}
	if req.ExpectedRevision != cur.Revision {
		return FactorCandidate{}, errRevisionConflict
	}
	name := strings.TrimSpace(req.Name)
	if n := runeLen(name); n < 1 || n > 80 {
		return FactorCandidate{}, fmt.Errorf("候选名称需要 1～80 个字符")
	}
	if runeLen(req.Notes) > 2000 {
		return FactorCandidate{}, fmt.Errorf("备注过长（上限 2000 字符）")
	}
	if err := validateCandidateUse(req.Use); err != nil {
		return FactorCandidate{}, err
	}
	if req.Status != CandidateStatusCandidate && req.Status != CandidateStatusArchived {
		return FactorCandidate{}, fmt.Errorf("候选状态无效: %s", req.Status)
	}
	if req.Use.Mode == "range" {
		if req.Use.Filter.Kind != cur.Factor.Kind ||
			req.Use.Filter.Days != cur.Factor.Days ||
			req.Use.Filter.FactorVersion != cur.Factor.ImplementationVersion {
			return FactorCandidate{}, fmt.Errorf("过滤配置与候选因子不一致（kind/days/version 必须与候选一致）")
		}
	}

	next := cur
	next.Revision = cur.Revision + 1
	next.Name = name
	next.Use = req.Use
	next.Status = req.Status
	next.Notes = req.Notes
	next.UpdatedAt = s.now().Format(time.RFC3339)
	if err := s.writeRevision(next, evidence); err != nil {
		return FactorCandidate{}, err
	}
	return next, nil
}

// findByRequestID 按幂等键扫描最新修订（数据量小，不做双写失真索引）。
func (s *CandidateStore) findByRequestID(requestID string) (*FactorCandidate, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() || !validCandidateID(e.Name()) {
			continue
		}
		c, err := s.readLatest(e.Name())
		if err != nil {
			if errors.Is(err, errCandidateNotFound) {
				continue
			}
			return nil, err
		}
		if c.CreateRequestID == requestID {
			cc := c
			return &cc, nil
		}
	}
	return nil, nil
}

// maxRevision 找到最大合法修订号（忽略 .tmp 与 *.analysis.json）。
func (s *CandidateStore) maxRevision(id string) (int, error) {
	revsDir := filepath.Join(s.root, id, "revisions")
	entries, err := os.ReadDir(revsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, errCandidateNotFound
		}
		return 0, err
	}
	maxRev := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasSuffix(e.Name(), ".analysis.json") {
			continue
		}
		if rev, ok := parseRevName(e.Name()); ok && rev > maxRev {
			maxRev = rev
		}
	}
	if maxRev == 0 {
		return 0, errCandidateNotFound
	}
	return maxRev, nil
}

// parseRevName 解析 NNNNNN.json 修订名。
func parseRevName(name string) (int, bool) {
	base := strings.TrimSuffix(name, ".json")
	if len(base) != 6 {
		return 0, false
	}
	rev, err := strconv.Atoi(base)
	if err != nil {
		return 0, false
	}
	return rev, true
}

// readLatest 读取最新修订。
func (s *CandidateStore) readLatest(id string) (FactorCandidate, error) {
	c, _, err := s.readLatestFull(id)
	return c, err
}

// readLatestFull 读取最新修订及其原始证据字节（更新时原样复制）。
func (s *CandidateStore) readLatestFull(id string) (FactorCandidate, []byte, error) {
	maxRev, err := s.maxRevision(id)
	if err != nil {
		return FactorCandidate{}, nil, err
	}
	return s.readRevision(id, maxRev)
}

// readRevision 读取并校验单个修订：候选记录 → 证据文件 → SHA-256 → 摘要一致。
// 损坏返回错误，不回退上一修订冒充最新。
func (s *CandidateStore) readRevision(id string, rev int) (FactorCandidate, []byte, error) {
	revName := fmt.Sprintf("%06d", rev)
	base := filepath.Join(s.root, id, "revisions")
	recPath := filepath.Join(base, revName+".json")
	evPath := filepath.Join(base, revName+".analysis.json")
	if !withinRoot(s.root, recPath) || !withinRoot(s.root, evPath) {
		return FactorCandidate{}, nil, fmt.Errorf("候选 ID 派生路径越界")
	}
	recData, err := os.ReadFile(recPath)
	if err != nil {
		return FactorCandidate{}, nil, err
	}
	var c FactorCandidate
	if err := json.Unmarshal(recData, &c); err != nil {
		return FactorCandidate{}, nil, fmt.Errorf("候选记录损坏: %w", err)
	}
	evData, err := os.ReadFile(evPath)
	if err != nil {
		return FactorCandidate{}, nil, err
	}
	sum := sha256.Sum256(evData)
	if hex.EncodeToString(sum[:]) != c.Evidence.ReportSHA256 {
		return FactorCandidate{}, nil, fmt.Errorf("候选证据哈希不匹配（最新写入不可信）")
	}
	var rep AnalysisReport
	if err := json.Unmarshal(evData, &rep); err != nil {
		return FactorCandidate{}, nil, fmt.Errorf("候选证据损坏: %w", err)
	}
	if err := verifyEvidenceMatches(c.Evidence, &rep); err != nil {
		return FactorCandidate{}, nil, err
	}
	return c, evData, nil
}

// writeRevision 写入单个修订：先写证据快照，后写候选记录（记录为提交标记）。
// 写入前再次校验证据字节与记录的 SHA-256 一致。
func (s *CandidateStore) writeRevision(c FactorCandidate, evidence []byte) error {
	if len(evidence) == 0 {
		return fmt.Errorf("证据快照为空")
	}
	sum := sha256.Sum256(evidence)
	if hex.EncodeToString(sum[:]) != c.Evidence.ReportSHA256 {
		return fmt.Errorf("证据哈希不一致，拒绝写入")
	}
	revName := fmt.Sprintf("%06d", c.Revision)
	base := filepath.Join(s.root, c.ID, "revisions")
	if !withinRoot(s.root, base) {
		return fmt.Errorf("候选 ID 派生路径越界")
	}
	if err := os.MkdirAll(base, 0755); err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(base, revName+".analysis.json"), evidence); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(base, revName+".json"), buf)
}

// verifyEvidenceMatches 校验证据摘要与完整报告关键字段一致。
func verifyEvidenceMatches(ev CandidateEvidence, rep *AnalysisReport) error {
	switch {
	case ev.AnalysisID != rep.AnalysisID:
	case ev.AnalysisVersion != rep.AnalysisVersion:
	case ev.Window != rep.Window:
	case !reflect.DeepEqual(ev.Range, rep.Range):
	case !reflect.DeepEqual(ev.Grouping, rep.Grouping):
	case !reflect.DeepEqual(ev.Stats, rep.Stats):
	case !reflect.DeepEqual(ev.Summary, rep.Summary):
	case !reflect.DeepEqual(ev.Coverage, rep.Coverage):
	case !reflect.DeepEqual(ev.YearCoverage, rep.YearCoverage):
	case ev.FirstDataDate != rep.FirstDataDate:
	case ev.LastDataDate != rep.LastDataDate:
	case ev.FinishedAt != rep.FinishedAt:
	default:
		return nil
	}
	return fmt.Errorf("候选证据摘要与完整报告不一致")
}
