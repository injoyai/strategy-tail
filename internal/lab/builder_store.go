package lab

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// builder_store.go 搭建器策略库存储（设计文档 §5）。
//
// 目录布局（root 默认 data/lab/strategies，相对路径随工作目录解析）：
//
//	<root>/st_<UTC时间戳>_<8hex>.json   # 策略定义（原子写，含独立版本号）
//
// 已保存策略与编辑草稿分离：加载返回可编辑副本；保存才落盘；
// 复制生成新 ID，不修改源策略。写入沿用 atomicWrite + withinRoot 模式。

// builderStrategyIDRe 策略 ID 格式：st_<UTC yyyyMMddTHHmmssSSSZ>_<8 hex>。
var builderStrategyIDRe = regexp.MustCompile(`^st_\d{8}T\d{9}Z_[0-9a-f]{8}$`)

// errBuilderNotFound 策略不存在。
var errBuilderNotFound = errors.New("策略不存在")

// errBuilderVersionConflict 乐观锁冲突：策略已被其他保存更新。
var errBuilderVersionConflict = errors.New("策略已被其他会话保存更新，请重新加载后再保存")

// BuilderStrategy 策略库条目：元信息 + 完整条件树定义。
type BuilderStrategy struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Version   int         `json:"version"` // 每次保存 +1，更新请求必须携带已读到的版本
	CreatedAt time.Time   `json:"createdAt"`
	UpdatedAt time.Time   `json:"updatedAt"`
	Spec      BuilderSpec `json:"spec"`
}

// BuilderStore 搭建器策略库存储。
type BuilderStore struct {
	root string
	mu   sync.Mutex
}

// NewBuilderStore 创建策略库存储。生产默认 root 为 data/lab/strategies；
// 测试必须使用 t.TempDir()。
func NewBuilderStore(root string) *BuilderStore {
	return &BuilderStore{root: root}
}

// newBuilderStrategyID 生成策略 ID（crypto/rand，不用全局 math/rand）。
func newBuilderStrategyID(now time.Time, random io.Reader) (string, error) {
	if random == nil {
		random = rand.Reader
	}
	return newPrefixedID("st", now, random)
}

// strategyPath 策略文件路径（ID 派生，越界拒绝）。
func (s *BuilderStore) strategyPath(id string) (string, error) {
	if !builderStrategyIDRe.MatchString(id) {
		return "", fmt.Errorf("非法策略 ID: %q", id)
	}
	p := filepath.Join(s.root, id+".json")
	if !withinRoot(s.root, p) {
		return "", fmt.Errorf("策略 ID 派生路径越界")
	}
	return p, nil
}

// readStrategy 读取并解析单个策略文件；spec 版本高于已知版本明确拒绝。
func readStrategy(path string) (*BuilderStrategy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errBuilderNotFound
		}
		return nil, err
	}
	var st BuilderStrategy
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("策略文件损坏: %w", err)
	}
	if st.Spec.Version > builderSpecV1 {
		return nil, fmt.Errorf("策略定义版本 %d 高于已知最高版本 %d，请升级程序后再读取",
			st.Spec.Version, builderSpecV1)
	}
	if !builderStrategyIDRe.MatchString(st.ID) {
		return nil, fmt.Errorf("策略文件内 ID 非法: %q", st.ID)
	}
	return &st, nil
}

// writeStrategy 原子写策略文件。
func (s *BuilderStore) writeStrategy(st *BuilderStrategy) error {
	if err := os.MkdirAll(s.root, 0755); err != nil {
		return err
	}
	p, err := s.strategyPath(st.ID)
	if err != nil {
		return err
	}
	buf, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(p, buf)
}

// List 返回全部策略（UpdatedAt 倒序）。损坏的正式策略文件 fail closed；
// .tmp 临时文件与非策略文件忽略。
func (s *BuilderStore) List() ([]BuilderStrategy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]BuilderStrategy, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".tmp-") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		if !builderStrategyIDRe.MatchString(id) {
			continue
		}
		p := filepath.Join(s.root, e.Name())
		if !withinRoot(s.root, p) {
			continue
		}
		st, err := readStrategy(p)
		if err != nil {
			return nil, fmt.Errorf("策略 %s 读取失败: %w", id, err)
		}
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

// Get 按 ID 读取策略；ID 不合法视为不存在（读路径统一 404 语义，且不向
// 调用方泄露 ID 格式细节，调用方用 errors.Is(err, errBuilderNotFound) 判定）。
func (s *BuilderStore) Get(id string) (*BuilderStrategy, error) {
	if !builderStrategyIDRe.MatchString(id) {
		return nil, fmt.Errorf("%w（非法策略 ID）", errBuilderNotFound)
	}
	p, err := s.strategyPath(id)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return readStrategy(p)
}

// Create 以草稿 spec 新建策略：校验后生成新 ID，版本 1。
func (s *BuilderStore) Create(spec BuilderSpec) (*BuilderStrategy, error) {
	if err := spec.Validate("save"); err != nil {
		return nil, err
	}
	now := time.Now()
	id, err := newBuilderStrategyID(now, nil)
	if err != nil {
		return nil, err
	}
	st := &BuilderStrategy{
		ID:        id,
		Name:      spec.Name,
		Version:   1,
		CreatedAt: now,
		UpdatedAt: now,
		Spec:      spec,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writeStrategy(st); err != nil {
		return nil, err
	}
	return st, nil
}

// Update 覆盖保存已有策略：expectedVersion 不匹配时拒绝（多页编辑不得悄悄覆盖新版本）。
func (s *BuilderStore) Update(id string, spec BuilderSpec, expectedVersion int) (*BuilderStrategy, error) {
	if err := spec.Validate("save"); err != nil {
		return nil, err
	}
	p, err := s.strategyPath(id)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	current, err := readStrategy(p)
	if err != nil {
		return nil, err
	}
	if current.Version != expectedVersion {
		return nil, errBuilderVersionConflict
	}
	current.Name = spec.Name
	current.Version++
	current.UpdatedAt = time.Now()
	current.Spec = spec
	if err := s.writeStrategy(current); err != nil {
		return nil, err
	}
	return current, nil
}
