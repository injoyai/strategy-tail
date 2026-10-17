package lab

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/lab/builder"
)

// builder_server.go 搭建器（自由组合策略）REST API（设计文档 §5）。
//
//	GET  /api/builder/components              组件目录（可运行 + 不可用及原因）
//	GET  /api/builder/templates               内置模板
//	GET  /api/builder/strategies              策略库列表（UpdatedAt 倒序）
//	POST /api/builder/strategies              创建策略（201）
//	GET  /api/builder/strategies/{id}         读取策略
//	PUT  /api/builder/strategies/{id}         覆盖保存（expectedVersion 冲突 409）
//	POST /api/builder/strategies/{id}/copy    复制策略（201，源策略不变）
//	POST /api/builder/run                     启动搭建器回测（请求问题 400，任务互斥 409）

// builderDataDir 策略库存储目录（沿用 data/lab/* 布局，相对工作目录解析）。
const builderDataDir = "data/lab/strategies"

// handleBuilderComponents 组件目录：前端条件编辑器的唯一事实来源。
func (s *Server) handleBuilderComponents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, builder.Catalog())
}

// handleBuilderTemplates 内置模板列表。
func (s *Server) handleBuilderTemplates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"templates": BuilderTemplates()})
}

// handleBuilderListStrategies 策略库列表。
func (s *Server) handleBuilderListStrategies(w http.ResponseWriter, r *http.Request) {
	list, err := s.builderStore.List()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取策略列表失败: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"strategies": list})
}

// handleBuilderGetStrategy 读取单个策略（可编辑副本，含当前版本号）。
func (s *Server) handleBuilderGetStrategy(w http.ResponseWriter, r *http.Request) {
	st, ok := s.getBuilderStrategy(w, r.PathValue("id"))
	if !ok {
		return
	}
	writeJSON(w, map[string]any{"strategy": st})
}

// getBuilderStrategy 读取策略并统一映射错误；失败时已写响应并返回 false。
func (s *Server) getBuilderStrategy(w http.ResponseWriter, id string) (*BuilderStrategy, bool) {
	st, err := s.builderStore.Get(id)
	if err != nil {
		if errors.Is(err, errBuilderNotFound) {
			writeErr(w, http.StatusNotFound, "策略不存在")
			return nil, false
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	return st, true
}

// handleBuilderCreateStrategy 新建策略（spec.Name 即策略名，版本 1）。
func (s *Server) handleBuilderCreateStrategy(w http.ResponseWriter, r *http.Request) {
	if !requireSameOrigin(w, r) {
		return
	}
	var req struct {
		Spec BuilderSpec `json:"spec"`
	}
	if !decodeStrict(r, w, &req, maxCandidateBody) {
		return
	}
	st, err := s.builderStore.Create(req.Spec)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{"strategy": st})
}

// handleBuilderUpdateStrategy 覆盖保存：expectedVersion 与存储版本不符 → 409，
// 多页编辑不得悄悄覆盖其他会话保存的新版本。
func (s *Server) handleBuilderUpdateStrategy(w http.ResponseWriter, r *http.Request) {
	if !requireSameOrigin(w, r) {
		return
	}
	id := r.PathValue("id")
	var req struct {
		Spec            BuilderSpec `json:"spec"`
		ExpectedVersion int         `json:"expectedVersion"`
	}
	if !decodeStrict(r, w, &req, maxCandidateBody) {
		return
	}
	st, err := s.builderStore.Update(id, req.Spec, req.ExpectedVersion)
	if err != nil {
		switch {
		case errors.Is(err, errBuilderVersionConflict):
			writeErr(w, http.StatusConflict, err.Error())
		case errors.Is(err, errBuilderNotFound):
			writeErr(w, http.StatusNotFound, "策略不存在")
		default:
			writeErr(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	writeJSON(w, map[string]any{"strategy": st})
}

// handleBuilderCopyStrategy 复制策略：新 ID、版本 1；源策略与既有报告不变。
func (s *Server) handleBuilderCopyStrategy(w http.ResponseWriter, r *http.Request) {
	if !requireSameOrigin(w, r) {
		return
	}
	id := r.PathValue("id")
	var req struct {
		Name string `json:"name"`
	}
	if !decodeStrict(r, w, &req, maxCandidateBody) {
		return
	}
	st, err := s.builderStore.Copy(id, req.Name)
	if err != nil {
		if errors.Is(err, errBuilderNotFound) {
			writeErr(w, http.StatusNotFound, "策略不存在")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{"strategy": st})
}

// handleBuilderRun 启动搭建器回测：条件树校验（mode=run）→ 构建买入变体
// （单变体，报告快照经 Report.Config.Builder 自动持久化完整 spec）。
func (s *Server) handleBuilderRun(w http.ResponseWriter, r *http.Request) {
	if !requireSameOrigin(w, r) {
		return
	}
	var cfg RunConfig
	if !decodeStrict(r, w, &cfg, maxCandidateBody) {
		return
	}
	if cfg.Builder == nil {
		writeErr(w, http.StatusBadRequest, "请提供搭建器策略定义")
		return
	}
	if err := cfg.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	buyer, err := cfg.Builder.Buyer()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	variants := []core.Variant{{Name: cfg.Builder.Name, Buyer: buyer}}
	cfg.ScriptName = sourceBuilder
	if err := s.runner.Start(cfg, variants); err != nil {
		// 走到这里说明请求已全部校验通过，剩余错误只有任务互斥
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "variants": 1, "source": sourceBuilder})
}
