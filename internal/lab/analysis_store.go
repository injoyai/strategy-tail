package lab

import (
	"crypto/rand"
	"encoding/hex"
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

	"github.com/injoyai/goutil/oss/csv"
)

// analysis_store.go 不可变分析历史存储。
//
// 目录布局（root 默认 output/factor）：
//
//	<root>/<kind>/<analysisId>/report.json   # 不可变主报告（原子写）
//	<root>/<kind>/<analysisId>/ic.csv        # best-effort
//	<root>/<kind>/<analysisId>/report.html   # best-effort
//	<root>/<kind>/latest.json                # 该 kind 当前指针（原子写）
//	<root>/latest.json                       # 全局指针（含 kind，晚于 kind 指针写）
//	<root>/<kind>/report.json|ic.csv|report.html  # 兼容镜像（best-effort）
//
// 主报告或 latest 指针失败返回错误；兼容镜像失败只记录日志。所有由 ID 派生的
// 路径在使用前执行根目录约束检查，拒绝穿越。

// analysisIDRe 不可变分析 ID 格式：an_<UTC yyyyMMddTHHmmssSSSZ>_<8 hex>。
var analysisIDRe = regexp.MustCompile(`^an_\d{8}T\d{9}Z_[0-9a-f]{8}$`)

// kindDirRe 合法 kind 目录（registry kind 均为小写字母/数字/下划线）。
var kindDirRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// latestFile 每 kind 与全局的指针文件名。
const latestFile = "latest.json"

// analysisVersionMaxKnown 已知最高分析报告版本（v4：研究协议 + 多周期）。
const analysisVersionMaxKnown = 4

// evidenceLegacy 无协议旧报告的证据等级摘要值：真实等级为 exploratory +
// same_close_to_close_legacy（设计 §14），由展示层按该值提示。
const evidenceLegacy = "legacy"

// errAnalysisVersionUnsupported 报告版本高于已知最高版本，拒绝读取。
var errAnalysisVersionUnsupported = errors.New("不支持的分析报告版本")

// analysisEvidenceClass 报告证据等级摘要：v4 取协议派生值；旧报告返回
// evidenceLegacy。
func analysisEvidenceClass(rep *AnalysisReport) string {
	if rep.Protocol == nil {
		return evidenceLegacy
	}
	if rep.EvidenceClass != "" {
		return rep.EvidenceClass
	}
	return deriveEvidenceClass(*rep.Protocol)
}

// errAnalysisNotFound 指定分析报告不存在。
var errAnalysisNotFound = errors.New("分析报告不存在")

// newPrefixedID 生成 <prefix>_<UTC yyyyMMddTHHmmssSSSZ>_<8 hex> 形式 ID。
// random 为 nil 时使用 crypto/rand.Reader；不得使用全局 math/rand。
func newPrefixedID(prefix string, now time.Time, random io.Reader) (string, error) {
	if random == nil {
		random = rand.Reader
	}
	b := make([]byte, 4)
	if _, err := io.ReadFull(random, b); err != nil {
		return "", err
	}
	ts := now.UTC().Format("20060102T150405.000Z")
	ts = strings.Replace(ts, ".", "", 1) // yyyyMMddTHHmmssSSSZ
	return prefix + "_" + ts + "_" + hex.EncodeToString(b), nil
}

// newAnalysisID 生成不可变分析 ID。
func newAnalysisID(now time.Time, random io.Reader) (string, error) {
	return newPrefixedID("an", now, random)
}

// validAnalysisID 严格校验分析 ID 格式。
func validAnalysisID(id string) bool { return analysisIDRe.MatchString(id) }

// generateAnalysisID 使用系统随机源的便捷入口（生产路径调用）。
func generateAnalysisID() (string, error) {
	return newAnalysisID(time.Now(), rand.Reader)
}

// validKindDir 校验 kind 可用于目录名。
func validKindDir(kind string) bool { return kindDirRe.MatchString(kind) }

// withinRoot 确认 path 仍在 root 之内（拒绝 .. 穿越）。
func withinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// atomicWrite 同目录临时文件 + rename 原子写。
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp := filepath.Join(dir, ".tmp-"+filepath.Base(path))
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// analysisLatest latest 指针内容：全局与每 kind 共用结构。
type analysisLatest struct {
	AnalysisID string `json:"analysisId"`
	Kind       string `json:"kind"`
	Report     string `json:"report"` // "<analysisId>/report.json" 兼容字段
}

// AnalysisSummary 分析历史摘要（列表 API 使用，不含完整报告）。
type AnalysisSummary struct {
	AnalysisID      string `json:"analysisId"`
	Kind            string `json:"kind"`
	FactorName      string `json:"factorName"`
	AnalysisVersion int    `json:"analysisVersion"`
	FinishedAt      string `json:"finishedAt"`
	FirstDataDate   string `json:"firstDataDate"`
	LastDataDate    string `json:"lastDataDate"`
	// EvidenceClass 证据等级摘要：v4 报告取协议派生值（exploratory |
	// retrospective）；无协议的旧报告为 "legacy"（展示层按旧标签提示
	// exploratory + same_close_to_close_legacy）。
	EvidenceClass string `json:"evidenceClass"`
}

// AnalysisStore 不可变分析历史存储。
type AnalysisStore struct {
	root string
	mu   sync.Mutex
}

// NewAnalysisStore 创建分析历史存储。生产默认 root 为 output/factor；
// 测试必须使用 t.TempDir()。
func NewAnalysisStore(root string) *AnalysisStore {
	return &AnalysisStore{root: root}
}

// Save 按写入顺序发布一份分析报告：
// 1. 创建 <kind>/<analysisId>/；2. 原子写主 report.json；
// 3. best-effort ic.csv 与 report.html；4. 原子更新 <kind>/latest.json；
// 5. 原子更新全局 latest.json；6. best-effort 兼容镜像。
// 主报告或任一 latest 指针失败返回错误；兼容镜像失败仅记录日志。
func (s *AnalysisStore) Save(rep *AnalysisReport) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if rep == nil {
		return fmt.Errorf("分析报告为空")
	}
	kind := rep.Kind
	if !validKindDir(kind) {
		return fmt.Errorf("非法因子类型目录: %q", kind)
	}
	id := rep.AnalysisID
	if !validAnalysisID(id) {
		return fmt.Errorf("非法分析 ID: %q", id)
	}

	dir := filepath.Join(s.root, kind, id)
	if !withinRoot(s.root, dir) {
		return fmt.Errorf("分析 ID 派生路径越界")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	buf, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(dir, "report.json"), buf); err != nil {
		return err
	}

	// best-effort 补充产物：失败不影响主报告与指针
	_ = atomicWrite(filepath.Join(dir, "ic.csv"), renderAnalysisCSV(rep))
	_ = atomicWrite(filepath.Join(dir, "report.html"), renderAnalysisHTML(rep))

	ptr := analysisLatest{AnalysisID: id, Kind: kind, Report: id + "/report.json"}
	ptrBuf, _ := json.Marshal(ptr)
	// 每 kind 指针先写，全局指针（含 kind）后写：现有无参数 /latest 可确定性恢复
	if err := atomicWrite(filepath.Join(s.root, kind, latestFile), ptrBuf); err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(s.root, latestFile), ptrBuf); err != nil {
		return err
	}

	// best-effort 兼容镜像：旧固定路径与人工读取不立即失效
	kindDir := filepath.Join(s.root, kind)
	_ = atomicWrite(filepath.Join(kindDir, "report.json"), buf)
	_ = atomicWrite(filepath.Join(kindDir, "ic.csv"), renderAnalysisCSV(rep))
	_ = atomicWrite(filepath.Join(kindDir, "report.html"), renderAnalysisHTML(rep))
	return nil
}

// readReportFile 读取并解析指定 report.json；损坏返回错误（fail closed）。
// 高于已知版本的报告以明确 unsupported 错误拒绝，不按旧版本猜测读取。
func readReportFile(path string) (*AnalysisReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rep AnalysisReport
	if err := json.Unmarshal(data, &rep); err != nil {
		return nil, err
	}
	if rep.AnalysisVersion > analysisVersionMaxKnown {
		return nil, fmt.Errorf("报告版本 %d 高于已知最高版本 %d: %w",
			rep.AnalysisVersion, analysisVersionMaxKnown, errAnalysisVersionUnsupported)
	}
	return &rep, nil
}

// readLatest 读取指针文件；不存在返回 nil, nil。
func (s *AnalysisStore) readLatest(path string) (*analysisLatest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ptr analysisLatest
	if err := json.Unmarshal(data, &ptr); err != nil {
		return nil, fmt.Errorf("latest 指针损坏: %w", err)
	}
	if !validAnalysisID(ptr.AnalysisID) || !validKindDir(ptr.Kind) {
		return nil, fmt.Errorf("latest 指针内容非法")
	}
	return &ptr, nil
}

// Get 按 ID 返回不可变报告（跨 kind 扫描，ID 全局唯一）。
func (s *AnalysisStore) Get(id string) (*AnalysisReport, error) {
	if !validAnalysisID(id) {
		return nil, fmt.Errorf("非法分析 ID: %q", id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errAnalysisNotFound
		}
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() || !validKindDir(e.Name()) {
			continue
		}
		p := filepath.Join(s.root, e.Name(), id, "report.json")
		if !withinRoot(s.root, p) {
			continue
		}
		rep, err := readReportFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("分析 %s 读取失败: %w", id, err)
		}
		return rep, nil
	}
	return nil, errAnalysisNotFound
}

// Latest 返回当前指针指向的报告：kind 为空读全局指针，否则读该 kind 指针。
// 全局指针自含 kind 与 analysisId，即使 kind 指针缺失仍可确定恢复。
// 无指针时返回 nil, nil（不随机扫描猜测）。
func (s *AnalysisStore) Latest(kind string) (*AnalysisReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if kind == "" {
		ptr, err := s.readLatest(filepath.Join(s.root, latestFile))
		if err != nil {
			return nil, err
		}
		if ptr == nil {
			return nil, nil
		}
		return s.readByKindAndID(ptr.Kind, ptr.AnalysisID)
	}
	if !validKindDir(kind) {
		return nil, fmt.Errorf("非法因子类型目录: %q", kind)
	}
	ptr, err := s.readLatest(filepath.Join(s.root, kind, latestFile))
	if err != nil {
		return nil, err
	}
	if ptr == nil {
		return nil, nil
	}
	return s.readByKindAndID(kind, ptr.AnalysisID)
}

// readByKindAndID 按 kind+id 读取不可变报告（供 latest 指针恢复）。
func (s *AnalysisStore) readByKindAndID(kind, id string) (*AnalysisReport, error) {
	if !validKindDir(kind) || !validAnalysisID(id) {
		return nil, fmt.Errorf("latest 指针内容非法")
	}
	dir := filepath.Join(s.root, kind, id)
	if !withinRoot(s.root, dir) {
		return nil, fmt.Errorf("latest 指针路径越界")
	}
	return readReportFile(filepath.Join(dir, "report.json"))
}

// List 扫描某 kind 下的历史报告摘要（时间倒序）。
// 只扫描合法 ID 目录与有效报告；遇到损坏的正式报告返回错误（fail closed），
// .tmp 临时文件与兼容镜像文件（非目录）被忽略。
func (s *AnalysisStore) List(kind string) ([]AnalysisSummary, error) {
	if !validKindDir(kind) {
		return nil, fmt.Errorf("非法因子类型目录: %q", kind)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Join(s.root, kind)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []AnalysisSummary
	for _, e := range entries {
		if !e.IsDir() || !validAnalysisID(e.Name()) {
			continue
		}
		p := filepath.Join(dir, e.Name(), "report.json")
		if !withinRoot(s.root, p) {
			return nil, fmt.Errorf("分析 ID 派生路径越界: %s", e.Name())
		}
		rep, err := readReportFile(p)
		if err != nil {
			return nil, fmt.Errorf("分析 %s 损坏: %w", e.Name(), err)
		}
		out = append(out, AnalysisSummary{
			AnalysisID:      rep.AnalysisID,
			Kind:            rep.Kind,
			FactorName:      rep.FactorName,
			AnalysisVersion: rep.AnalysisVersion,
			FinishedAt:      rep.FinishedAt,
			FirstDataDate:   rep.FirstDataDate,
			LastDataDate:    rep.LastDataDate,
			EvidenceClass:   analysisEvidenceClass(rep),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FinishedAt > out[j].FinishedAt })
	return out, nil
}

// renderAnalysisCSV 逐日 IC CSV（无效日按 0 写），失败返回 nil。
func renderAnalysisCSV(rep *AnalysisReport) []byte {
	rows := [][]any{{"date", "ic"}}
	for _, d := range rep.Daily {
		v := 0.0
		if d.IC != nil {
			v = *d.IC
		}
		rows = append(rows, []any{d.Date, v})
	}
	if buf, err := csv.Export(rows); err == nil {
		return buf.Bytes()
	}
	return nil
}

// renderAnalysisHTML 逐日 IC 折线页（无效日为 null，前端 connectNulls 补线）。
func renderAnalysisHTML(rep *AnalysisReport) []byte {
	days := make([]string, 0, len(rep.Daily))
	ics := make([]*float64, 0, len(rep.Daily))
	for _, d := range rep.Daily {
		days = append(days, d.Date)
		ics = append(ics, d.IC)
	}
	dayJSON, _ := json.Marshal(days)
	icJSON, _ := json.Marshal(ics)
	return []byte(fmt.Sprintf(analysisHTML, rep.FactorName, rep.FactorName, rep.Window,
		rep.Stats.Mean, rep.Stats.Std, rep.Stats.TStat, rep.Stats.Pairs, dayJSON, icJSON))
}
