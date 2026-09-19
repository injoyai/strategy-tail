package lab

import (
	"crypto/rand"
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

// factor_model_store.go v2 Task 1 模型 revision 追加式存储（设计 §12.1）。
//
// 目录布局（root 生产默认 data/lab/factor-models）：
//
//	<root>/<modelId>/NNNNNN.json   # 每个 revision 一个文件，只追加不覆盖
//	<root>/<modelId>/archive.json  # 归档标记（含 archivedAt），不删除 revision
//
// 契约：服务端 ID、规范化 modelHash、临时文件 + rename 原子写、安全根目录
// 检查、只追加 revision、读取时重算 modelHash（不匹配 fail closed）、损坏
// JSON fail closed、旧 revision 始终可读；归档不破坏历史引用。

// 模型库错误（API 层映射状态码）。
var (
	errFactorModelNotFound         = errors.New("模型不存在")
	errFactorModelRevisionConflict = errors.New("模型已被更新，请重新加载后再保存")
)

// DefaultFactorModelRoot 生产默认模型目录（data/ 为本地持久数据，不进 Git）。
func DefaultFactorModelRoot() string { return filepath.Join("data", "lab", "factor-models") }

// FactorModelStore 模型追加式 revision 存储。Create 幂等（同 RequestID 同
// 请求 hash 返回已有记录；同 ID 不同 hash 冲突）；并发写经互斥锁 + 基础
// revision 乐观校验，绝不覆盖既有 revision。
type FactorModelStore struct {
	root string
	mu   sync.Mutex
	now  func() time.Time
	rand io.Reader
}

// NewFactorModelStore 创建模型存储；测试必须使用 t.TempDir()。
func NewFactorModelStore(root string) *FactorModelStore {
	return &FactorModelStore{root: root, now: time.Now, rand: rand.Reader}
}

// factorModelArchive archive.json 标记内容。
type factorModelArchive struct {
	ModelID    string `json:"modelId"`
	ArchivedAt string `json:"archivedAt"`
}

// Create 创建模型或追加 revision。input 为上游验证读取器（装配时重新读取
// 并校验全部上游验证 hash）。created=true 表示新建/新 revision。
func (s *FactorModelStore) Create(req CreateFactorModelRequest, input ValidationInput) (portfolioresearch.FactorModel, bool, error) {
	norm, err := normalizeCreateFactorModelRequest(req)
	if err != nil {
		return portfolioresearch.FactorModel{}, false, err
	}
	reqHash, err := createFactorModelRequestHash(norm)
	if err != nil {
		return portfolioresearch.FactorModel{}, false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 幂等扫描：RequestID 命中且请求 hash 一致 → 返回已有记录。
	existing, err := s.findByRequestIDLocked(norm.RequestID)
	if err != nil {
		return portfolioresearch.FactorModel{}, false, err
	}
	if existing != nil {
		if existing.CreateRequestHash == reqHash {
			return *existing, false, nil
		}
		return portfolioresearch.FactorModel{}, false, errIdempotencyConflict
	}

	// 追加 revision：模型必须存在且基础 revision 等于当前最新（乐观并发）。
	var modelID string
	var revision int
	if norm.ModelID != "" {
		latest, err := s.readLatestLocked(norm.ModelID)
		if err != nil {
			return portfolioresearch.FactorModel{}, false, err
		}
		if latest.Revision != norm.BaseRevision {
			return portfolioresearch.FactorModel{}, false, errFactorModelRevisionConflict
		}
		modelID = norm.ModelID
		revision = norm.BaseRevision + 1
	} else {
		id, err := newModelID(s.now(), s.rand)
		if err != nil {
			return portfolioresearch.FactorModel{}, false, fmt.Errorf("生成模型 ID 失败: %w", err)
		}
		modelID = id
		revision = 1
	}

	m, err := assembleFactorModel(norm, input, s.now())
	if err != nil {
		return portfolioresearch.FactorModel{}, false, err
	}
	m.ModelID = modelID
	m.Revision = revision
	m.CreateRequestID = norm.RequestID
	m.CreateRequestHash = reqHash
	hash, err := portfolioresearch.ModelHash(m)
	if err != nil {
		return portfolioresearch.FactorModel{}, false, err
	}
	m.ModelHash = hash
	// 写入前完整校验（含证据等级 = 最弱输入、资源限制），fail closed。
	if err := m.Validate(portfolioresearch.DefaultModelLimits()); err != nil {
		return portfolioresearch.FactorModel{}, false, err
	}

	dir := filepath.Join(s.root, modelID)
	if !withinRoot(s.root, dir) {
		return portfolioresearch.FactorModel{}, false, fmt.Errorf("模型 ID 派生路径越界")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return portfolioresearch.FactorModel{}, false, err
	}
	buf, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return portfolioresearch.FactorModel{}, false, err
	}
	if err := atomicWrite(filepath.Join(dir, fmt.Sprintf("%06d.json", revision)), buf); err != nil {
		return portfolioresearch.FactorModel{}, false, err
	}
	return m, true, nil
}

// Get 读取指定模型指定 revision（含 modelHash 重算校验）。旧 revision 永远
// 可读——revision 文件只追加不覆盖。
func (s *FactorModelStore) Get(modelID string, revision int) (portfolioresearch.FactorModel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readRevisionLocked(modelID, revision)
}

// GetLatest 读取指定模型最新 revision。
func (s *FactorModelStore) GetLatest(modelID string) (portfolioresearch.FactorModel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	maxRev, err := s.maxRevisionLocked(modelID)
	if err != nil {
		return portfolioresearch.FactorModel{}, err
	}
	return s.readRevisionLocked(modelID, maxRev)
}

// List 列出全部模型最新 revision。includeArchived=false 隐藏已归档模型。
// 排序：CreatedAt 倒序，ID 升序兜底稳定。损坏记录 fail closed。
func (s *FactorModelStore) List(includeArchived bool) ([]portfolioresearch.FactorModel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []portfolioresearch.FactorModel
	for _, e := range entries {
		if !e.IsDir() || !validModelID(e.Name()) {
			continue
		}
		if !includeArchived && s.archivedLocked(e.Name()) {
			continue
		}
		m, err := s.readLatestLocked(e.Name())
		if err != nil {
			if errors.Is(err, errFactorModelNotFound) {
				continue // 空目录/未提交完成，忽略
			}
			return nil, err // 损坏 fail closed
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].ModelID < out[j].ModelID
	})
	return out, nil
}

// Archive 归档模型：只写 archive.json 标记（含 archivedAt），不删除任何
// revision、不破坏历史引用；已归档时幂等返回（保留首次归档时间）。
func (s *FactorModelStore) Archive(modelID string) error {
	if !validModelID(modelID) {
		return fmt.Errorf("非法模型 ID: %q", modelID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.readLatestLocked(modelID); err != nil {
		return err
	}
	if s.archivedLocked(modelID) {
		return nil
	}
	dir := filepath.Join(s.root, modelID)
	if !withinRoot(s.root, dir) {
		return fmt.Errorf("模型 ID 派生路径越界")
	}
	mark := factorModelArchive{ModelID: modelID, ArchivedAt: s.now().UTC().Format(time.RFC3339)}
	buf, err := json.MarshalIndent(mark, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, "archive.json"), buf)
}

// ArchivedAt 返回模型归档时间（RFC3339）；未归档返回空字符串。
func (s *FactorModelStore) ArchivedAt(modelID string) (string, error) {
	if !validModelID(modelID) {
		return "", fmt.Errorf("非法模型 ID: %q", modelID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.readLatestLocked(modelID); err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(s.root, modelID, "archive.json"))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var mark factorModelArchive
	if err := json.Unmarshal(data, &mark); err != nil {
		return "", fmt.Errorf("归档标记损坏: %w", err)
	}
	if mark.ModelID != modelID {
		return "", fmt.Errorf("归档标记 ID 与目录不一致")
	}
	return mark.ArchivedAt, nil
}

// archivedLocked 归档标记探测（调用方已持锁）。
func (s *FactorModelStore) archivedLocked(modelID string) bool {
	_, err := os.Stat(filepath.Join(s.root, modelID, "archive.json"))
	return err == nil
}

// findByRequestIDLocked 按幂等键扫描各模型的全部 revision 文件（目录内按
// revision 文件名逐一读取并检查 CreateRequestID）。追加 revision 会把最新
// 记录的幂等键更新为追加请求 ID，若只扫最新 revision，旧请求重放会漏扫并
// 静默新建模型，违反"同 RequestID 同 hash 幂等返回已有记录"契约。
func (s *FactorModelStore) findByRequestIDLocked(requestID string) (*portfolioresearch.FactorModel, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() || !validModelID(e.Name()) {
			continue
		}
		revs, err := s.revisionNumbersLocked(e.Name())
		if err != nil {
			return nil, err
		}
		for _, rev := range revs {
			m, err := s.readRevisionLocked(e.Name(), rev)
			if err != nil {
				if errors.Is(err, errFactorModelNotFound) {
					continue
				}
				return nil, err // 损坏 fail closed
			}
			if m.CreateRequestID == requestID {
				mm := m
				return &mm, nil
			}
		}
	}
	return nil, nil
}

// maxRevisionLocked 找到最大合法修订号（忽略 archive.json 与 .tmp-*）。
func (s *FactorModelStore) maxRevisionLocked(modelID string) (int, error) {
	revs, err := s.revisionNumbersLocked(modelID)
	if err != nil {
		return 0, err
	}
	return revs[len(revs)-1], nil
}

// revisionNumbersLocked 返回模型目录内全部合法修订号（升序，忽略目录、
// archive.json 与 .tmp-*）。模型不存在/无任何修订时返回 errFactorModelNotFound。
func (s *FactorModelStore) revisionNumbersLocked(modelID string) ([]int, error) {
	dir := filepath.Join(s.root, modelID)
	if !withinRoot(s.root, dir) {
		return nil, fmt.Errorf("模型 ID 派生路径越界")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errFactorModelNotFound
		}
		return nil, err
	}
	var revs []int
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if rev, ok := parseRevName(e.Name()); ok {
			revs = append(revs, rev)
		}
	}
	if len(revs) == 0 {
		return nil, errFactorModelNotFound
	}
	sort.Ints(revs)
	return revs, nil
}

// readLatestLocked 读取最新修订。
func (s *FactorModelStore) readLatestLocked(modelID string) (portfolioresearch.FactorModel, error) {
	maxRev, err := s.maxRevisionLocked(modelID)
	if err != nil {
		return portfolioresearch.FactorModel{}, err
	}
	return s.readRevisionLocked(modelID, maxRev)
}

// readRevisionLocked 读取并校验单个修订：ID/revision 与路径一致、modelHash
// 重算一致。损坏 fail closed，不回退上一修订冒充最新。
func (s *FactorModelStore) readRevisionLocked(modelID string, revision int) (portfolioresearch.FactorModel, error) {
	if !validModelID(modelID) {
		return portfolioresearch.FactorModel{}, fmt.Errorf("非法模型 ID: %q", modelID)
	}
	if revision < 1 {
		return portfolioresearch.FactorModel{}, fmt.Errorf("revision 无效: %d（应为 >=1）", revision)
	}
	path := filepath.Join(s.root, modelID, fmt.Sprintf("%06d.json", revision))
	if !withinRoot(s.root, path) {
		return portfolioresearch.FactorModel{}, fmt.Errorf("模型 ID 派生路径越界")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return portfolioresearch.FactorModel{}, errFactorModelNotFound
		}
		return portfolioresearch.FactorModel{}, err
	}
	var m portfolioresearch.FactorModel
	if err := json.Unmarshal(data, &m); err != nil {
		return portfolioresearch.FactorModel{}, fmt.Errorf("模型记录损坏: %w", err)
	}
	if m.ModelID != modelID || m.Revision != revision {
		return portfolioresearch.FactorModel{}, fmt.Errorf("模型记录 ID/revision 与路径不一致")
	}
	hash, err := portfolioresearch.ModelHash(m)
	if err != nil {
		return portfolioresearch.FactorModel{}, err
	}
	if hash != m.ModelHash {
		return portfolioresearch.FactorModel{}, fmt.Errorf("模型哈希不匹配（记录不可信）")
	}
	// 读取时再次完整校验（含证据等级 = 最弱输入、枚举白名单、资源限制），
	// 与 v1 readRequest 同模式：即使语义 hash 未覆盖的派生字段被篡改也
	// fail closed。
	if err := m.Validate(portfolioresearch.DefaultModelLimits()); err != nil {
		return portfolioresearch.FactorModel{}, fmt.Errorf("模型记录非法: %w", err)
	}
	return m, nil
}
