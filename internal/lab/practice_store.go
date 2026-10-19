package lab

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/lab/builder"
)

// practice_store.go 盘感练习存储：目录布局、原子发布、幂等创建、
// 命令应用与重练。布局与并发契约见 docs/plans/2026-10-18-trading-practice.md §3：
//
//	data/lab/practice/<opaque-id>/market.json   # 不可变行情与指标快照
//	data/lab/practice/<opaque-id>/session.json  # 会话状态（原子写）
//
// 发布顺序为先写 market.json 再原子发布 session.json：崩溃窗口内会话仍是
// preparing，重启恢复标为中断，不会出现引用缺失行情的 active 会话。
// 首版面向单个 Lab 服务实例：Store 互斥锁串行化读改写，不宣称多实例共享写。
// 所有由 ID 派生的路径执行根目录约束检查；损坏或未知版本数据报错，不补造。

// practiceDataDir 练习记录根目录（相对服务工作目录，惯例同 builderDataDir）。
const practiceDataDir = "data/lab/practice"

const (
	practiceSessionFile = "session.json"
	practiceMarketFile  = "market.json"
)

// practiceIDRe 练习 ID 格式：practice_<UTC yyyyMMddTHHmmssSSSZ>_<8 hex>，
// 不含股票、信号日期等身份信息。
var practiceIDRe = regexp.MustCompile(`^practice_\d{8}T\d{9}Z_[0-9a-f]{8}$`)

// 存储层领域错误。
var (
	errPracticeRequestIDRequired = errors.New("requestId 不能为空")
	errPracticeRequestConflict   = errors.New("requestId 已用于其他出题参数")
)

func validPracticeID(id string) bool { return practiceIDRe.MatchString(id) }

// PracticeSummary 练习历史摘要（列表 API 使用）。不含股票身份、真实日期、
// 私有路径或未来行情；策略名是用户自选内容，可以展示。
type PracticeSummary struct {
	ID              string  `json:"id"`
	Phase           string  `json:"phase"`
	Stage           string  `json:"stage,omitempty"`
	StrategyID      string  `json:"strategyId"`
	StrategyName    string  `json:"strategyName"`
	FromYear        int     `json:"fromYear"`
	ToYear          int     `json:"toYear"`
	CreatedAt       string  `json:"createdAt"`
	UpdatedAt       string  `json:"updatedAt"`
	EndReason       string  `json:"endReason,omitempty"`
	FailReason      string  `json:"failReason,omitempty"`
	SourceSessionID string  `json:"sourceSessionId,omitempty"` // 非空表示重练记录
	RealizedPnL     float64 `json:"realizedPnl"`               // 仅已卖出产生（元）；保留打平的零值
	RealizedPct     float64 `json:"realizedPct"`
}

// PracticeStore 盘感练习存储。每次操作从磁盘读取最新状态、在锁内变更并
// 原子写回，不设内存缓存：服务重启后从磁盘天然恢复。
type PracticeStore struct {
	root string
	mu   sync.Mutex
}

// NewPracticeStore 创建练习存储；root 一般为 practiceDataDir，测试用 t.TempDir()。
func NewPracticeStore(root string) *PracticeStore {
	return &PracticeStore{root: root}
}

// generateID 生成练习 ID（crypto/rand，无全局随机源）。
func (s *PracticeStore) generateID() (string, error) {
	return newPrefixedID("practice", time.Now(), nil)
}

// practiceDir 校验 ID 并返回记录目录（拒绝路径穿越）。
func (s *PracticeStore) practiceDir(id string) (string, error) {
	if !validPracticeID(id) {
		return "", fmt.Errorf("非法练习 ID: %q", id)
	}
	dir := filepath.Join(s.root, id)
	if !withinRoot(s.root, dir) {
		return "", fmt.Errorf("练习 ID 派生路径越界")
	}
	return dir, nil
}

// CreatePreparing 幂等创建 preparing 会话：同 requestID 且参数一致返回已有
// 记录；参数不一致返回冲突，不静默新建。费用在此刻冻结为快照（滑点清零），
// 后续修改费用配置不影响本记录。
func (s *PracticeStore) CreatePreparing(strategyID string, strategyVersion int, spec builder.BuilderSpec, cost core.Cost, fromYear, toYear int, requestID string) (*practiceSession, error) {
	if requestID == "" {
		return nil, errPracticeRequestIDRequired
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, err := s.findByRequestIDLocked(requestID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.StrategyID != strategyID || existing.StrategyVersion != strategyVersion ||
			existing.FromYear != fromYear || existing.ToYear != toYear {
			return nil, fmt.Errorf("%w: requestId=%s", errPracticeRequestConflict, requestID)
		}
		return existing, nil
	}

	id, err := s.generateID()
	if err != nil {
		return nil, err
	}
	dir, err := s.practiceDir(id)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	now := practiceNowUTC()
	sess := &practiceSession{
		Version:         practiceFormatV1,
		ID:              id,
		Revision:        0,
		Phase:           practicePhasePreparing,
		StrategyID:      strategyID,
		StrategyVersion: strategyVersion,
		StrategySpec:    spec,
		Cost:            practiceCostSnapshot(cost),
		BudgetYuan:      practiceBudgetYuan,
		FromYear:        fromYear,
		ToYear:          toYear,
		RequestID:       requestID,
		CreatedAt:       now,
		UpdatedAt:       now,
		BuyIndex:        -1,
		SellIndex:       -1,
	}
	if err := s.writeSessionLocked(sess); err != nil {
		return nil, err
	}
	return sess, nil
}

// findStartRequest 先查已受理请求，重试不依赖当前任务锁或策略库新版本。
func (s *PracticeStore) findStartRequest(req practiceStartRequest) (*practiceSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, err := s.findByRequestIDLocked(req.RequestID)
	if err != nil || existing == nil {
		return existing, err
	}
	if existing.SourceSessionID != "" || existing.StrategyID != req.StrategyID || existing.StrategyVersion != req.StrategyVersion ||
		existing.FromYear != req.FromYear || existing.ToYear != req.ToYear {
		return nil, errPracticeRequestConflict
	}
	return existing, nil
}

// Publish 发布抽题结果：先原子写 market.json，再把会话推进为 active。
// 仅 preparing 会话可发布；发布后 revision=1，当前位置为信号日。
func (s *PracticeStore) Publish(id string, m *practiceMarket, signalIndex int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, _, err := s.getLocked(id)
	if err != nil {
		return err
	}
	if sess.Phase != practicePhasePreparing {
		return fmt.Errorf("会话阶段为 %s，无法发布题目", sess.Phase)
	}
	if m == nil || len(m.Days) == 0 {
		return fmt.Errorf("%w: 行情快照为空", errPracticeCorrupt)
	}
	if signalIndex < 0 || signalIndex >= len(m.Days) {
		return fmt.Errorf("%w: 信号索引越界", errPracticeCorrupt)
	}
	m.Version = practiceFormatV1
	buf, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	dir, err := s.practiceDir(id)
	if err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(dir, practiceMarketFile), buf); err != nil {
		return err
	}
	sess.Phase = practicePhaseActive
	sess.Stage = practiceStageWaiting
	sess.Revision = 1
	sess.SignalIndex = signalIndex
	sess.CurrentIndex = signalIndex
	sess.Quantity = 0
	sess.BuyIndex = -1
	sess.BuyCostYuan = 0
	sess.SellIndex = -1
	sess.SellIncomeYuan = 0
	sess.EndReason = ""
	sess.UpdatedAt = practiceNowUTC()
	return s.writeSessionLocked(sess)
}

// MarkFailed 将 preparing 会话标为失败（出题任务错误路径）。reason 为
// practiceFail* 原因码，不含股票身份或原始行情错误详情。
func (s *PracticeStore) MarkFailed(id, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, _, err := s.getLocked(id)
	if err != nil {
		return err
	}
	if sess.Phase != practicePhasePreparing {
		return fmt.Errorf("会话阶段为 %s，无法标记失败", sess.Phase)
	}
	sess.Phase = practicePhaseFailed
	sess.FailReason = reason
	sess.UpdatedAt = practiceNowUTC()
	return s.writeSessionLocked(sess)
}

// MarkCancelled 将 preparing 会话标为已取消（用户主动停止出题）。
func (s *PracticeStore) MarkCancelled(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, _, err := s.getLocked(id)
	if err != nil {
		return err
	}
	if sess.Phase != practicePhasePreparing {
		return fmt.Errorf("会话阶段为 %s，无法取消", sess.Phase)
	}
	sess.Phase = practicePhaseCancelled
	sess.UpdatedAt = practiceNowUTC()
	return s.writeSessionLocked(sess)
}

// RecoverInterrupted 服务启动时调用：将遗留 preparing 会话标为
// failed(interrupted)——重启后没有执行者的出题不伪装为可继续练习，
// 用户可显式重新出题。崩溃窗口内已写入的 market.json 随记录一起废弃。
func (s *PracticeStore) RecoverInterrupted() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ids, err := s.listIDsLocked()
	if err != nil {
		return err
	}
	for _, id := range ids {
		sess, _, err := s.getLocked(id)
		if err != nil {
			return err
		}
		if sess.Phase != practicePhasePreparing {
			continue
		}
		sess.Phase = practicePhaseFailed
		sess.FailReason = practiceFailInterrupt
		sess.UpdatedAt = practiceNowUTC()
		if err := s.writeSessionLocked(sess); err != nil {
			return err
		}
	}
	return nil
}

// Get 读取会话与行情快照。active/finished 必须有行情快照；
// preparing/failed/cancelled 尚未（或不再）关联可用行情，返回 nil。
func (s *PracticeStore) Get(id string) (*practiceSession, *practiceMarket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getLocked(id)
}

// Apply 在 Store 锁内读取、校验、应用命令并原子写回。
// 幂等重放（同 commandId 同动作）返回 changed=false 及当前持久状态作为
// 权威结果；错误时状态不变。写盘失败时磁盘保持旧状态，内存副本丢弃。
func (s *PracticeStore) Apply(id string, cmd practiceCommand) (*practiceSession, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, m, err := s.getLocked(id)
	if err != nil {
		return nil, false, err
	}
	changed, err := sess.applyCommand(m, cmd)
	if err != nil {
		return nil, false, err
	}
	if changed {
		sess.UpdatedAt = practiceNowUTC()
		if err := s.writeSessionLocked(sess); err != nil {
			return nil, false, err
		}
	}
	return sess, changed, nil
}

// List 返回练习历史摘要（创建时间倒序；ID 时间戳保证排序稳定）。
func (s *PracticeStore) List() ([]PracticeSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ids, err := s.listIDsLocked()
	if err != nil {
		return nil, err
	}
	out := make([]PracticeSummary, 0, len(ids))
	for _, id := range ids {
		sess, _, err := s.getLocked(id)
		if err != nil {
			return nil, err
		}
		sum := PracticeSummary{
			ID:              sess.ID,
			Phase:           sess.Phase,
			Stage:           sess.Stage,
			StrategyID:      sess.StrategyID,
			StrategyName:    sess.StrategySpec.Name,
			FromYear:        sess.FromYear,
			ToYear:          sess.ToYear,
			CreatedAt:       sess.CreatedAt,
			UpdatedAt:       sess.UpdatedAt,
			EndReason:       sess.EndReason,
			FailReason:      sess.FailReason,
			SourceSessionID: sess.SourceSessionID,
		}
		if sess.Quantity > 0 && sess.Stage == practiceStageSold {
			sum.RealizedPnL = sess.SellIncomeYuan - sess.BuyCostYuan
			if sess.BuyCostYuan > 0 {
				sum.RealizedPct = sum.RealizedPnL / sess.BuyCostYuan * 100
			}
		}
		out = append(out, sum)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

// Retry 重练：仅 finished 记录可重练。复制原行情快照为新记录，策略／费用／
// 预算沿用开题快照，当前位置回到信号日、无成交，保存 sourceSessionId；
// 不重新扫描筛题。同 requestID 重试返回已创建的重练记录（幂等）。
func (s *PracticeStore) Retry(id, requestID string) (*practiceSession, error) {
	if requestID == "" {
		return nil, errPracticeRequestIDRequired
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	orig, m, err := s.getLocked(id)
	if err != nil {
		return nil, err
	}
	if orig.Phase != practicePhaseFinished {
		return nil, errPracticeNotFinished
	}
	existing, err := s.findByRequestIDLocked(requestID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.SourceSessionID != id {
			return nil, errPracticeRequestConflict
		}
		return existing, nil
	}
	if orig.LastRetryRequestID == requestID && orig.LastRetrySessionID != "" {
		sess, _, err := s.getLocked(orig.LastRetrySessionID)
		return sess, err
	}
	if m == nil || len(m.Days) == 0 {
		return nil, fmt.Errorf("%w: 原题行情快照缺失", errPracticeCorrupt)
	}

	newID, err := s.generateID()
	if err != nil {
		return nil, err
	}
	newDir, err := s.practiceDir(newID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(newDir, 0755); err != nil {
		return nil, err
	}
	// 原样复制行情快照字节：重练与原题使用完全相同的历史数据。
	marketBuf, err := os.ReadFile(filepath.Join(s.root, id, practiceMarketFile))
	if err != nil {
		return nil, err
	}
	if err := atomicWrite(filepath.Join(newDir, practiceMarketFile), marketBuf); err != nil {
		return nil, err
	}

	now := practiceNowUTC()
	sess := &practiceSession{
		Version:         practiceFormatV1,
		ID:              newID,
		Revision:        1,
		Phase:           practicePhaseActive,
		Stage:           practiceStageWaiting,
		StrategyID:      orig.StrategyID,
		StrategyVersion: orig.StrategyVersion,
		StrategySpec:    orig.StrategySpec,
		Cost:            orig.Cost,
		BudgetYuan:      orig.BudgetYuan,
		FromYear:        orig.FromYear,
		ToYear:          orig.ToYear,
		RequestID:       requestID,
		CreatedAt:       now,
		UpdatedAt:       now,
		SignalIndex:     orig.SignalIndex,
		CurrentIndex:    orig.SignalIndex,
		BuyIndex:        -1,
		SellIndex:       -1,
		SourceSessionID: orig.ID,
	}
	if err := s.writeSessionLocked(sess); err != nil {
		return nil, err
	}

	// 在原题上记录重练幂等键。不递增 revision：重练簿记不影响命令并发语义。
	orig.LastRetryRequestID = requestID
	orig.LastRetrySessionID = newID
	orig.UpdatedAt = now
	if err := s.writeSessionLocked(orig); err != nil {
		return nil, err
	}
	return sess, nil
}

// getLocked 按 ID 读取会话（及 active/finished 的行情快照）并做一致性检查。
func (s *PracticeStore) getLocked(id string) (*practiceSession, *practiceMarket, error) {
	dir, err := s.practiceDir(id)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", errPracticeNotFound, err)
	}
	sess, err := readPracticeSession(filepath.Join(dir, practiceSessionFile))
	if err != nil {
		return nil, nil, err
	}
	var m *practiceMarket
	if sess.Phase == practicePhaseActive || sess.Phase == practicePhaseFinished {
		m, err = readPracticeMarket(filepath.Join(dir, practiceMarketFile))
		if err != nil {
			return nil, nil, err
		}
		if err := validatePracticeState(sess, m); err != nil {
			return nil, nil, err
		}
	}
	return sess, m, nil
}

// writeSessionLocked 原子写会话文件。
func (s *PracticeStore) writeSessionLocked(sess *practiceSession) error {
	dir, err := s.practiceDir(sess.ID)
	if err != nil {
		return err
	}
	buf, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, practiceSessionFile), buf)
}

// listIDsLocked 列出全部合法练习 ID（无记录时返回 nil）。
func (s *PracticeStore) listIDsLocked() ([]string, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() && validPracticeID(e.Name()) {
			ids = append(ids, e.Name())
		}
	}
	sort.Strings(ids) // ID 含 UTC 时间戳，字典序即时间序
	return ids, nil
}

// findByRequestIDLocked 扫描各记录查找 requestID 命中的会话。
// 记录数量为个人练习量级，逐一读取成本可接受；无 session.json 的目录
// （创建中途崩溃残留）跳过，交给恢复逻辑与列表容错处理。
func (s *PracticeStore) findByRequestIDLocked(requestID string) (*practiceSession, error) {
	ids, err := s.listIDsLocked()
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		dir, err := s.practiceDir(id)
		if err != nil {
			continue
		}
		path := filepath.Join(dir, practiceSessionFile)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		sess, err := readPracticeSession(path)
		if err != nil {
			return nil, err
		}
		if sess.RequestID == requestID {
			return sess, nil
		}
	}
	return nil, nil
}

// readPracticeSession 读取并校验会话文件；损坏或未知版本报错，不猜测。
func readPracticeSession(path string) (*practiceSession, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errPracticeNotFound
		}
		return nil, err
	}
	var sess practiceSession
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("%w: 会话解析失败", errPracticeCorrupt)
	}
	if sess.Version != practiceFormatV1 {
		return nil, fmt.Errorf("%w: 会话版本 %d 不受支持", errPracticeCorrupt, sess.Version)
	}
	if sess.ID != filepath.Base(filepath.Dir(path)) {
		return nil, fmt.Errorf("%w: 会话身份与目录不一致", errPracticeCorrupt)
	}
	return &sess, nil
}

// readPracticeMarket 读取并校验行情快照；快照必须非空。
func readPracticeMarket(path string) (*practiceMarket, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: 行情快照缺失", errPracticeCorrupt)
		}
		return nil, err
	}
	var m practiceMarket
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%w: 行情快照解析失败", errPracticeCorrupt)
	}
	if m.Version != practiceFormatV1 {
		return nil, fmt.Errorf("%w: 行情快照版本 %d 不受支持", errPracticeCorrupt, m.Version)
	}
	if len(m.Days) == 0 {
		return nil, fmt.Errorf("%w: 行情快照为空", errPracticeCorrupt)
	}
	return &m, nil
}

// validatePracticeState 读取后的一致性检查（fail closed）：索引必须落在
// 快照范围内且满足时序约束，防止损坏数据把用户引入越界状态。
func validatePracticeState(sess *practiceSession, m *practiceMarket) error {
	last := len(m.Days) - 1
	if sess.SignalIndex < 0 || sess.SignalIndex > last {
		return fmt.Errorf("%w: 信号索引越界", errPracticeCorrupt)
	}
	if sess.CurrentIndex < sess.SignalIndex || sess.CurrentIndex > last {
		return fmt.Errorf("%w: 当前索引越界", errPracticeCorrupt)
	}
	if sess.BuyIndex < -1 || sess.BuyIndex > last ||
		(sess.BuyIndex >= 0 && sess.BuyIndex < sess.SignalIndex) {
		return fmt.Errorf("%w: 买入索引越界", errPracticeCorrupt)
	}
	if sess.SellIndex < -1 || sess.SellIndex > last ||
		(sess.SellIndex >= 0 && sess.SellIndex < sess.BuyIndex) {
		return fmt.Errorf("%w: 卖出索引越界", errPracticeCorrupt)
	}
	return nil
}
