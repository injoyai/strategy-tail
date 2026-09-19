package lab

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// factor_trial_store.go 追加式试验账本存储（设计 §10.1/§12.1，计划 Task 6
// Step 2）。目录布局（root 默认 data/lab/factor-trials）：
//
//	<root>/<trialId>.request.json  # Start 写入，之后永不修改
//	<root>/<trialId>.result.json   # Finish 一次写入，之后永不修改
//
// 请求与结果分文件避免覆盖：完整 trial 由二者组合读取；结果文件缺失即
// running。损坏的账本文件 fail closed 返回错误，不静默跳过。

const (
	trialRequestSuffix = ".request.json"
	trialResultSuffix  = ".result.json"
)

// DefaultTrialRoot 生产默认试验账本目录。
func DefaultTrialRoot() string { return filepath.Join("data", "lab", "factor-trials") }

// TrialStore 追加式试验账本存储。
type TrialStore struct {
	root string
	mu   sync.Mutex
}

// NewTrialStore 创建试验账本存储；测试必须使用 t.TempDir()。
func NewTrialStore(root string) *TrialStore { return &TrialStore{root: root} }

func (s *TrialStore) requestPath(id string) string {
	return filepath.Join(s.root, id+trialRequestSuffix)
}

func (s *TrialStore) resultPath(id string) string {
	return filepath.Join(s.root, id+trialResultSuffix)
}

// Start 登记一次专业模式运行的请求记录（status=running，永不修改）。
// FamilyID/VariantID 来自协议并进入 ProtocolHash（对协议整体计算，与报告
// hash 同函数同值）。登记失败返回错误，由调用方决定是否中止分析。
func (s *TrialStore) Start(protocol ResearchProtocol, factor FactorSnapshot, analysisID string) (FactorTrial, error) {
	if err := protocol.Validate(); err != nil {
		return FactorTrial{}, fmt.Errorf("试验协议非法: %w", err)
	}
	if !validAnalysisID(analysisID) {
		return FactorTrial{}, fmt.Errorf("非法分析 ID: %q", analysisID)
	}
	hash, err := protocolHash(protocol)
	if err != nil {
		return FactorTrial{}, fmt.Errorf("协议 hash 失败: %w", err)
	}
	now := time.Now()
	id, err := newTrialID(now, nil)
	if err != nil {
		return FactorTrial{}, fmt.Errorf("生成试验 ID 失败: %w", err)
	}
	t := FactorTrial{
		ID:           id,
		FamilyID:     protocol.Trial.FamilyID,
		VariantID:    protocol.Trial.VariantID,
		AnalysisID:   analysisID,
		Factor:       factor,
		ProtocolHash: hash,
		StartedAt:    now.UTC().Format(time.RFC3339),
		Status:       TrialStatusRunning,
	}
	buf, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return FactorTrial{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.root, 0755); err != nil {
		return FactorTrial{}, err
	}
	path := s.requestPath(id)
	if !withinRoot(s.root, path) {
		return FactorTrial{}, fmt.Errorf("试验 ID 派生路径越界")
	}
	if err := atomicWrite(path, buf); err != nil {
		return FactorTrial{}, err
	}
	return t, nil
}

// Finish 写入独立结果文件完成试验。FinishedAt 由服务端时间戳填充，忽略
// 调用方传入值；请求文件不存在或结果文件已存在（追加式不允许覆盖）均拒绝。
// 失败、取消与无样本运行同样必须 Finish（设计 §10.1）。
func (s *TrialStore) Finish(id string, result TrialResult) error {
	if !validTrialID(id) {
		return fmt.Errorf("非法试验 ID: %q", id)
	}
	if !finishedTrialStatuses[result.Status] {
		return fmt.Errorf("结果状态非法: %q（running 不是完成态）", result.Status)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	reqPath := s.requestPath(id)
	if !withinRoot(s.root, reqPath) {
		return fmt.Errorf("试验 ID 派生路径越界")
	}
	if _, err := os.Stat(reqPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("试验 %s 无请求记录，拒绝完成", id)
		}
		return err
	}
	resPath := s.resultPath(id)
	if _, err := os.Stat(resPath); err == nil {
		return fmt.Errorf("试验 %s 已有结果记录，拒绝覆盖（追加式账本）", id)
	} else if !os.IsNotExist(err) {
		return err
	}
	out := result
	out.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	buf, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(resPath, buf)
}

// ListFamily 返回指定家族的全部 trial（包含所有状态：running 与四种终态），
// 按 StartedAt 升序、ID 兜底稳定排序——冻结验证时清单可复现。
// 损坏的请求或结果文件返回错误（fail closed）；目录不存在返回 nil, nil。
func (s *TrialStore) ListFamily(familyID string) ([]FactorTrial, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []FactorTrial
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, trialRequestSuffix) {
			continue // 目录与 .tmp-* 等非请求文件忽略
		}
		id := strings.TrimSuffix(name, trialRequestSuffix)
		if !validTrialID(id) {
			continue
		}
		reqPath := s.requestPath(id)
		if !withinRoot(s.root, reqPath) {
			return nil, fmt.Errorf("试验 ID 派生路径越界: %s", id)
		}
		data, err := os.ReadFile(reqPath)
		if err != nil {
			return nil, fmt.Errorf("试验 %s 请求读取失败: %w", id, err)
		}
		var t FactorTrial
		if err := json.Unmarshal(data, &t); err != nil {
			return nil, fmt.Errorf("试验 %s 请求损坏: %w", id, err)
		}
		if t.FamilyID != familyID {
			continue
		}
		// 组合结果文件：缺失即 running（请求文件状态永不修改）。
		resData, err := os.ReadFile(s.resultPath(id))
		switch {
		case err == nil:
			var res TrialResult
			if err := json.Unmarshal(resData, &res); err != nil {
				return nil, fmt.Errorf("试验 %s 结果损坏: %w", id, err)
			}
			if !finishedTrialStatuses[res.Status] {
				return nil, fmt.Errorf("试验 %s 结果状态非法: %q", id, res.Status)
			}
			t.Status = res.Status
			t.FinishedAt = res.FinishedAt
			t.Message = res.Message
		case os.IsNotExist(err):
			// 保持 running 与零值 FinishedAt
		default:
			return nil, fmt.Errorf("试验 %s 结果读取失败: %w", id, err)
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartedAt != out[j].StartedAt {
			return out[i].StartedAt < out[j].StartedAt
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}
