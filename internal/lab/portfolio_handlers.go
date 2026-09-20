package lab

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/injoyai/strategy-tail/internal/portfolioresearch"
)

// portfolio_handlers.go v2 Task 9 HTTP API（设计 §12.2/§14/§15）。
//
// 完成门槛：handler 只做参数解析/输入校验/调用 Store 与 Runner/组装响应
// JSON；统计与交易核心逻辑全部在 portfolioresearch/lab 领域层，handler 不
// 含任何统计或交易计算。
//
// 资源上限（设计 §15：日期、数量、权重、阈值、数组长度可配置）。
const (
	// portfolioMinDate / portfolioMaxDate 日期资源上限（YYYY-MM-DD，字典序
	// = 时间序，可直接字符串比较）。
	portfolioMinDate = "1990-01-01"
	portfolioMaxDate = "2100-12-31"
	// maxListPageSize 列表每页条数上限（与各 Store 的 Max*PageSize 一致）。
	maxListPageSize = 200
)

// ---- 模型（factor-models） ----

// handleCreateFactorModel 创建模型或追加 revision：201 新建 / 200 幂等命中；
// 请求问题 400，幂等键复用与 revision 冲突 409。
func (s *Server) handleCreateFactorModel(w http.ResponseWriter, r *http.Request) {
	var req CreateFactorModelRequest
	if !decodeStrict(r, w, &req, maxCandidateBody) {
		return
	}
	if s.modelStore == nil {
		writeErr(w, http.StatusInternalServerError, "模型存储未注入")
		return
	}
	m, created, err := s.modelStore.Create(req, s.validationInput)
	if err != nil {
		switch {
		case errors.Is(err, errIdempotencyConflict), errors.Is(err, errFactorModelRevisionConflict):
			writeErr(w, http.StatusConflict, err.Error())
		default:
			writeErr(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	code := http.StatusOK
	if created {
		code = http.StatusCreated
	}
	writeJSONCode(w, code, map[string]any{"created": created, "model": m})
}

// handleListFactorModels 模型列表（服务端分页）：默认隐藏归档；includeArchived
// 只接受 true/false。page/pageSize 为可选查询参数（page>=1、pageSize 1-200，
// 与实验/验证列表一致，见 parsePageParams）；响应含分页元数据
// items/total/page/pageSize，并保留既有 models 字段——旧客户端不传分页参数
// 时行为不变（models 返回全量并带 total）。排序（CreatedAt 倒序 + ID 升序
// 兜底）由 Store 保证，页码越界返回空 items + 真实 total（页码可恢复）。
func (s *Server) handleListFactorModels(w http.ResponseWriter, r *http.Request) {
	includeArchived := false
	switch r.URL.Query().Get("includeArchived") {
	case "", "false":
	case "true":
		includeArchived = true
	default:
		writeErr(w, http.StatusBadRequest, "includeArchived 只接受 true/false")
		return
	}
	// 分页参数可选：均不传（旧客户端）→ 全量列表 + 分页元数据；任一带参 →
	// 服务端分页（缺省页补默认值，与实验/验证列表同款校验与越界语义）。
	hasPage := r.URL.Query().Get("page") != "" || r.URL.Query().Get("pageSize") != ""
	page, pageSize := 1, 0
	if hasPage {
		var ok bool
		page, pageSize, ok = parsePageParams(w, r)
		if !ok {
			return
		}
	}
	list, err := s.modelStore.List(includeArchived)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取模型列表失败")
		return
	}
	total := len(list)
	if !hasPage {
		writeJSON(w, map[string]any{
			"models": list, "items": list, "total": total, "page": 1, "pageSize": total,
		})
		return
	}
	start := (page - 1) * pageSize
	if start >= total {
		writeJSON(w, map[string]any{
			"models": []portfolioresearch.FactorModel{},
			"items":  []portfolioresearch.FactorModel{},
			"total":  total, "page": page, "pageSize": pageSize,
		})
		return
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	items := list[start:end]
	writeJSON(w, map[string]any{
		"models": items, "items": items, "total": total, "page": page, "pageSize": pageSize,
	})
}

// handleGetFactorModel 模型详情：默认最新 revision；?revision=N 读指定修订。
func (s *Server) handleGetFactorModel(w http.ResponseWriter, r *http.Request) {
	modelID := r.PathValue("modelId")
	if !validModelID(modelID) {
		writeErr(w, http.StatusBadRequest, "非法模型 ID")
		return
	}
	var m portfolioresearch.FactorModel
	var err error
	if revStr := r.URL.Query().Get("revision"); revStr != "" {
		rev, convErr := strconv.Atoi(revStr)
		if convErr != nil || rev < 1 {
			writeErr(w, http.StatusBadRequest, "revision 无效（应为 >=1 的整数）")
			return
		}
		m, err = s.modelStore.Get(modelID, rev)
	} else {
		m, err = s.modelStore.GetLatest(modelID)
	}
	if err != nil {
		if errors.Is(err, errFactorModelNotFound) {
			writeErr(w, http.StatusNotFound, "模型不存在: "+modelID)
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"model": m})
}

// ---- 实验（portfolio-experiments） ----

// handleCreateExperiment 创建实验（初始 queued）：201 新建 / 200 幂等命中。
// 输入校验含日期资源上限（设计 §15）。
func (s *Server) handleCreateExperiment(w http.ResponseWriter, r *http.Request) {
	var req CreatePortfolioExperimentRequest
	if !decodeStrict(r, w, &req, maxCandidateBody) {
		return
	}
	if err := validateExperimentDateLimits(req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.experimentStore == nil {
		writeErr(w, http.StatusInternalServerError, "实验存储未注入")
		return
	}
	exp, created, err := s.experimentStore.Create(req)
	if err != nil {
		if errors.Is(err, errIdempotencyConflict) {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	code := http.StatusOK
	if created {
		code = http.StatusCreated
	}
	writeJSONCode(w, code, map[string]any{"created": created, "experiment": exp})
}

// validateExperimentDateLimits 实验区间日期资源上限校验（fail closed）。
func validateExperimentDateLimits(req CreatePortfolioExperimentRequest) error {
	for name, rng := range map[string]DateRange{
		"studyRange": req.StudyRange,
		"trainRange": req.TrainRange,
		"testRange":  req.TestRange,
	} {
		if rng.Start == "" && rng.End == "" {
			continue
		}
		if rng.Start < portfolioMinDate || rng.End > portfolioMaxDate {
			return fmt.Errorf("%s 超出日期资源上限（%s ~ %s）", name, portfolioMinDate, portfolioMaxDate)
		}
	}
	return nil
}

// handleListExperiments 实验列表：服务端分页（page/pageSize），过滤
// familyId/modelId/status；稳定排序由 Store 保证。
func (s *Server) handleListExperiments(w http.ResponseWriter, r *http.Request) {
	page, pageSize, ok := parsePageParams(w, r)
	if !ok {
		return
	}
	filter := ExperimentFilter{
		FamilyID: r.URL.Query().Get("familyId"),
		ModelID:  r.URL.Query().Get("modelId"),
		Status:   r.URL.Query().Get("status"),
	}
	pg, err := s.experimentStore.List(filter, page, pageSize)
	if err != nil {
		if validationErr(err) {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "读取实验列表失败")
		return
	}
	writeJSON(w, map[string]any{"experiments": pg})
}

// handleGetExperiment 实验详情（含状态/进度/错误/产物清单，来自持久 Store）。
func (s *Server) handleGetExperiment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validExperimentID(id) {
		writeErr(w, http.StatusBadRequest, "非法实验 ID")
		return
	}
	exp, err := s.experimentStore.Get(id)
	if err != nil {
		if errors.Is(err, errExperimentNotFound) {
			writeErr(w, http.StatusNotFound, "实验不存在: "+id)
			return
		}
		// 记录损坏属服务器状态问题（fail closed），非客户端请求错误。
		writeErr(w, http.StatusInternalServerError, "读取实验记录失败")
		return
	}
	writeJSON(w, map[string]any{"experiment": exp})
}

// handleExperimentArtifact 产物安全读取：产物名白名单校验（路径穿越拒绝），
// 仅 completed 实验可读，读取前经 Store 哈希校验。
func (s *Server) handleExperimentArtifact(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	name := r.PathValue("name")
	if !validExperimentID(id) {
		writeErr(w, http.StatusBadRequest, "非法实验 ID")
		return
	}
	if err := ValidArtifactName(name); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	exp, err := s.experimentStore.Get(id)
	if err != nil {
		if errors.Is(err, errExperimentNotFound) {
			writeErr(w, http.StatusNotFound, "实验不存在")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if exp.Status != portfolioresearch.RunStateCompleted || exp.Manifest == nil {
		writeErr(w, http.StatusNotFound, "实验尚未完成，无产物可读")
		return
	}
	if exp.Manifest.hashOf(name) == "" {
		writeErr(w, http.StatusNotFound, "产物不存在")
		return
	}
	data, err := os.ReadFile(filepath.Join(s.experimentStore.artifactsRoot, id, name))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取产物失败")
		return
	}
	contentType := "application/json; charset=utf-8"
	if strings.HasSuffix(name, ".csv") {
		contentType = "text/csv; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Write(data)
}

// ---- 验证（portfolio-validations） ----

// handleCreateValidation 创建验证（冻结 spec）：201 新建 / 200 幂等命中；
// 窗口非法/缺模型引用 400，模型不存在 404，hash 不匹配与幂等键冲突 409。
func (s *Server) handleCreateValidation(w http.ResponseWriter, r *http.Request) {
	var req CreatePortfolioValidationRequest
	if !decodeStrict(r, w, &req, maxCandidateBody) {
		return
	}
	if s.validationStore == nil || s.modelStore == nil {
		writeErr(w, http.StatusInternalServerError, "验证/模型存储未注入")
		return
	}
	rec, created, err := s.validationStore.Create(req, s.modelStore)
	if err != nil {
		switch {
		case errors.Is(err, errIdempotencyConflict):
			writeErr(w, http.StatusConflict, err.Error())
		case strings.Contains(err.Error(), "模型 hash 不匹配"):
			writeErr(w, http.StatusConflict, err.Error())
		case errors.Is(err, errFactorModelNotFound):
			writeErr(w, http.StatusNotFound, "模型不存在")
		default:
			writeErr(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	code := http.StatusOK
	if created {
		code = http.StatusCreated
	}
	writeJSONCode(w, code, map[string]any{"created": created, "validation": rec})
}

// handleListValidations 验证列表：服务端分页，过滤 modelId/verdict/evidenceClass。
func (s *Server) handleListValidations(w http.ResponseWriter, r *http.Request) {
	page, pageSize, ok := parsePageParams(w, r)
	if !ok {
		return
	}
	filter := PortfolioValidationFilter{
		ModelID:       r.URL.Query().Get("modelId"),
		Verdict:       r.URL.Query().Get("verdict"),
		EvidenceClass: r.URL.Query().Get("evidenceClass"),
	}
	pg, err := s.validationStore.List(filter, page, pageSize)
	if err != nil {
		if validationErr(err) {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "读取验证列表失败")
		return
	}
	writeJSON(w, map[string]any{"validations": pg})
}

// handleGetValidation 验证详情（冻结记录 + 窗口 + 报告 + supersededBy）。
func (s *Server) handleGetValidation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validPortfolioValidationID(id) {
		writeErr(w, http.StatusBadRequest, "非法验证 ID")
		return
	}
	view, err := s.validationStore.Get(id)
	if err != nil {
		if errors.Is(err, errPortfolioValidationNotFound) {
			writeErr(w, http.StatusNotFound, "验证不存在: "+id)
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"validation": view})
}

// ---- 运行（portfolio-runs） ----

// handleStartPortfolioRun 启动组合任务：accepted 202（长任务提交，终态从
// Store 恢复）；任务互斥/重复启动 409；不存在 404；非法 ID 400。
func (s *Server) handleStartPortfolioRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.portfolioRunner == nil {
		writeErr(w, http.StatusInternalServerError, "组合执行器未注入")
		return
	}
	switch {
	case validExperimentID(id):
		if _, err := s.experimentStore.Get(id); err != nil {
			if errors.Is(err, errExperimentNotFound) {
				writeErr(w, http.StatusNotFound, "实验不存在: "+id)
				return
			}
			// 记录损坏属服务器状态问题（fail closed），非客户端请求错误。
			writeErr(w, http.StatusInternalServerError, "读取实验记录失败")
			return
		}
		if err := s.portfolioRunner.StartPortfolioExperiment(id); err != nil {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		writeJSONCode(w, http.StatusAccepted, map[string]any{"accepted": true, "runId": id, "task": taskPortfolioExperiment})
	case validPortfolioValidationID(id):
		if _, err := s.validationStore.Get(id); err != nil {
			if errors.Is(err, errPortfolioValidationNotFound) {
				writeErr(w, http.StatusNotFound, "验证不存在: "+id)
				return
			}
			// 记录损坏属服务器状态问题（fail closed），非客户端请求错误。
			writeErr(w, http.StatusInternalServerError, "读取验证记录失败")
			return
		}
		if err := s.portfolioRunner.StartPortfolioValidation(id); err != nil {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		writeJSONCode(w, http.StatusAccepted, map[string]any{"accepted": true, "runId": id, "task": taskPortfolioValidation})
	default:
		writeErr(w, http.StatusBadRequest, "非法运行 ID（应为 pe_* 实验或 pv_* 验证）")
	}
}

// handleStopPortfolioRun 停止组合任务（复用共享 stopCh；无任务时为空操作）。
func (s *Server) handleStopPortfolioRun(w http.ResponseWriter, r *http.Request) {
	if s.portfolioRunner != nil {
		s.portfolioRunner.Stop()
	}
	writeJSON(w, map[string]any{"ok": true})
}

// handleGetPortfolioRun 读取组合任务状态：来自持久 Store（重启后可恢复）。
func (s *Server) handleGetPortfolioRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	switch {
	case validExperimentID(id):
		exp, err := s.experimentStore.Get(id)
		if err != nil {
			if errors.Is(err, errExperimentNotFound) {
				writeErr(w, http.StatusNotFound, "实验不存在: "+id)
				return
			}
			// 记录损坏属服务器状态问题（fail closed），非客户端请求错误。
			writeErr(w, http.StatusInternalServerError, "读取实验记录失败")
			return
		}
		writeJSON(w, map[string]any{"runId": id, "task": taskPortfolioExperiment, "run": exp})
	case validPortfolioValidationID(id):
		view, err := s.validationStore.Get(id)
		if err != nil {
			if errors.Is(err, errPortfolioValidationNotFound) {
				writeErr(w, http.StatusNotFound, "验证不存在: "+id)
				return
			}
			// 记录损坏属服务器状态问题（fail closed），非客户端请求错误。
			writeErr(w, http.StatusInternalServerError, "读取验证记录失败")
			return
		}
		writeJSON(w, map[string]any{"runId": id, "task": taskPortfolioValidation, "run": view})
	default:
		writeErr(w, http.StatusBadRequest, "非法运行 ID")
	}
}

// ---- 辅助 ----

// writeJSONCode 指定状态码的 JSON 响应。
func writeJSONCode(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// parsePageParams 解析分页参数：page 从 1 起，pageSize 1-maxListPageSize；
// 缺省 page=1/pageSize=20。失败时已写错误并返回 false。
func parsePageParams(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	q := r.URL.Query()
	page, err := strconv.Atoi(q.Get("page"))
	if err != nil || q.Get("page") == "" {
		page = 1
	}
	pageSize, err := strconv.Atoi(q.Get("pageSize"))
	if err != nil || q.Get("pageSize") == "" {
		pageSize = 20
	}
	if page < 1 {
		writeErr(w, http.StatusBadRequest, "page 无效（应为 >=1）")
		return 0, 0, false
	}
	if pageSize < 1 || pageSize > maxListPageSize {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("pageSize 无效（应为 1-%d）", maxListPageSize))
		return 0, 0, false
	}
	return page, pageSize, true
}

// validationErr 判断错误是否为输入校验错误（400）而非内部错误（500）。
// 各 Store 的校验错误信息含"非法"/"无效"（稳定文案）。
func validationErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "非法") || strings.Contains(msg, "无效")
}
