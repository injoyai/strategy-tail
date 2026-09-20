package lab

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/lib/extend"
	f "github.com/injoyai/strategy-tail/strategies/factor"
)

// server.go REST API + 静态页服务。
//
// API 契约见设计文档 §5：
//
//	GET/PUT  /api/script        读/写脚本（strategies/script/matrix.go 单一事实源）
//	POST     /api/script/check  Yaegi 干跑校验
//	POST     /api/run           启动回测（单任务互斥）
//	GET      /api/status        进度查询
//	POST     /api/stop          停止任务
//	GET      /api/report/latest 最新报告
//	GET      /api/reports       历史报告列表
//	GET      /api/report/{id}   指定报告
//	GET      /api/kline/{code}  日K数据
//	GET      /api/factors       因子目录
//	POST     /api/analyze       启动因子分析（与回测共用任务互斥）
//	GET      /api/analysis/latest 最新分析报告（内存无报告时从磁盘 latest 指针恢复）
//	GET      /api/analyses?kind=  分析历史摘要（时间倒序）
//	GET      /api/analysis/{analysisId} 指定不可变分析报告
//	GET      /api/strategy-presets 简单模式预设策略目录
//	POST     /api/strategy/run  启动简单模式回测（声明式 StrategySpec；请求问题 400，任务互斥 409）
//	POST     /api/factor-candidates 创建候选（201 新建 / 200 幂等命中 / 409 冲突）
//	GET      /api/factor-candidates?includeArchived=  候选列表（默认隐藏归档）
//	GET      /api/factor-candidates/{id} 候选详情（含实时兼容状态）
//	PUT      /api/factor-candidates/{id} 追加候选修订（expectedRevision 冲突 409）

// ScriptPath 策略脚本路径（页面编辑器直接读写该文件）。
const ScriptPath = "strategies/script/matrix.go"

// Server lab 服务。
type Server struct {
	runner *Runner
	mux    *http.ServeMux
	// analysisStore 不可变分析历史（生产默认 output/factor；测试注入临时根）。
	analysisStore *AnalysisStore
	// candidateStore 候选因子追加式库（生产默认 data/lab/factor-candidates；测试注入临时根）。
	candidateStore *CandidateStore

	// 多因子组合研究 v2（Task 9）依赖：模型/实验/验证库与组合执行器。
	// validationInput 为 v1 验证输入适配器（模型装配时重读上游验证）。
	modelStore      *FactorModelStore
	experimentStore *PortfolioExperimentStore
	validationStore *PortfolioValidationStore
	portfolioRunner *Runner // 与 runner 同一实例（组合任务与回测/分析共享互斥）
	validationInput ValidationInput
}

// NewServer 创建服务并注册路由。
func NewServer() *Server {
	s := &Server{
		runner:         NewRunner(),
		mux:            http.NewServeMux(),
		analysisStore:  NewAnalysisStore(filepath.Join("output", "factor")),
		candidateStore: NewCandidateStore(candidateDataDir),
	}
	s.wirePortfolioStores()
	s.registerRoutes()
	return s
}

// wirePortfolioStores 注入组合研究依赖（Task 9）：模型/实验/验证库、
// v1 验证输入适配器与组合执行器（与 runner 同一实例）。
func (s *Server) wirePortfolioStores() {
	s.modelStore = NewFactorModelStore(DefaultFactorModelRoot())
	s.experimentStore = NewPortfolioExperimentStore(DefaultPortfolioExperimentRoot(), DefaultPortfolioArtifactRoot())
	s.validationStore = NewPortfolioValidationStore(DefaultPortfolioValidationRoot())
	s.validationInput = NewValidationStoreAdapter(NewValidationStore(DefaultValidationRoot()))
	s.portfolioRunner = s.runner
	s.runner.ConfigurePortfolio(s.modelStore, s.experimentStore, s.validationStore)
}

// newServerWithStores 仅供测试的构造函数：注入独立临时根，避免共享全局目录。
func newServerWithStores(analyses *AnalysisStore, candidates *CandidateStore) *Server {
	runner := NewRunner()
	runner.store = analyses // 分析写入与查询同一根
	s := &Server{
		runner:         runner,
		mux:            http.NewServeMux(),
		analysisStore:  analyses,
		candidateStore: candidates,
	}
	s.wirePortfolioStores()
	s.registerRoutes()
	return s
}

// registerRoutes 注册全部 REST 路由。
func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /api/script", s.handleGetScript)
	s.mux.HandleFunc("PUT /api/script", s.handlePutScript)
	s.mux.HandleFunc("POST /api/script/check", s.handleCheckScript)
	s.mux.HandleFunc("POST /api/run", s.handleRun)
	s.mux.HandleFunc("GET /api/status", s.handleStatus)
	s.mux.HandleFunc("POST /api/stop", s.handleStop)
	s.mux.HandleFunc("GET /api/report/latest", s.handleLatestReport)
	s.mux.HandleFunc("GET /api/reports", s.handleReports)
	s.mux.HandleFunc("GET /api/report/{id}", s.handleReport)
	s.mux.HandleFunc("GET /api/kline/{code}", s.handleKline)
	s.mux.HandleFunc("GET /api/factors", s.handleFactors)
	s.mux.HandleFunc("POST /api/analyze", s.handleAnalyze)
	s.mux.HandleFunc("GET /api/analysis/latest", s.handleLatestAnalysis)
	s.mux.HandleFunc("GET /api/analyses", s.handleAnalyses)
	s.mux.HandleFunc("GET /api/analysis/{analysisId}", s.handleAnalysis)
	s.mux.HandleFunc("GET /api/strategy-presets", s.handleStrategyPresets)
	s.mux.HandleFunc("POST /api/strategy/run", s.handleStrategyRun)
	s.mux.HandleFunc("POST /api/factor-candidates", s.handleCreateCandidate)
	s.mux.HandleFunc("GET /api/factor-candidates", s.handleListCandidates)
	s.mux.HandleFunc("GET /api/factor-candidates/{id}", s.handleGetCandidate)
	s.mux.HandleFunc("PUT /api/factor-candidates/{id}", s.handleUpdateCandidate)

	// 多因子组合研究 v2（Task 9，设计 §12.2）。
	s.mux.HandleFunc("POST /api/factor-models", s.handleCreateFactorModel)
	s.mux.HandleFunc("GET /api/factor-models", s.handleListFactorModels)
	s.mux.HandleFunc("GET /api/factor-models/{modelId}", s.handleGetFactorModel)
	s.mux.HandleFunc("POST /api/portfolio-experiments", s.handleCreateExperiment)
	s.mux.HandleFunc("GET /api/portfolio-experiments", s.handleListExperiments)
	s.mux.HandleFunc("GET /api/portfolio-experiments/{id}", s.handleGetExperiment)
	s.mux.HandleFunc("GET /api/portfolio-experiments/{id}/artifacts/{name}", s.handleExperimentArtifact)
	s.mux.HandleFunc("POST /api/portfolio-validations", s.handleCreateValidation)
	s.mux.HandleFunc("GET /api/portfolio-validations", s.handleListValidations)
	s.mux.HandleFunc("GET /api/portfolio-validations/{id}", s.handleGetValidation)
	s.mux.HandleFunc("POST /api/portfolio-runs/{id}/start", s.handleStartPortfolioRun)
	s.mux.HandleFunc("POST /api/portfolio-runs/{id}/stop", s.handleStopPortfolioRun)
	s.mux.HandleFunc("GET /api/portfolio-runs/{id}", s.handleGetPortfolioRun)
	s.mux.HandleFunc("GET /", s.handleIndex)
}

// Handler 返回 http.Handler（测试用 httptest 直连）。
func (s *Server) Handler() http.Handler { return s.mux }

// webFS 前端静态资源（go:embed，无构建链）。
//
//go:embed web
var webFS embed.FS

// handleIndex 静态资源服务（web/lab/，含 index.html 与 Vue 模块脚本）。
// 仅服务 web/lab 内的常规文件，防路径穿越；无构建链，直接读 embed。
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	embedName := "web/lab/" + name
	if strings.HasSuffix(embedName, "/") {
		http.NotFound(w, r)
		return
	}
	data, err := webFS.ReadFile(embedName)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasSuffix(embedName, ".html"):
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case strings.HasSuffix(embedName, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(embedName, ".js"):
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Write(data)
}

// handleGetScript 读取当前脚本。
func (s *Server) handleGetScript(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(ScriptPath)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取脚本失败: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"content": string(data)})
}

// handlePutScript 保存脚本（PUT 即落盘，无草稿态）。
func (s *Server) handlePutScript(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		writeErr(w, http.StatusBadRequest, "脚本内容为空")
		return
	}
	// 保存前先校验，避免存入无法编译的脚本覆盖可用版本
	if err := CheckScript(req.Content); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := os.WriteFile(ScriptPath, []byte(req.Content), 0644); err != nil {
		writeErr(w, http.StatusInternalServerError, "保存脚本失败: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// handleCheckScript Yaegi 干跑校验，返回编译错误。
func (s *Server) handleCheckScript(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效: "+err.Error())
		return
	}
	if err := CheckScript(req.Content); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// handleRun 启动回测：Yaegi 加载脚本（编译期报错）→ 校验变体 → 入队。
func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	var cfg RunConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效: "+err.Error())
		return
	}
	src, err := os.ReadFile(ScriptPath)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取脚本失败: "+err.Error())
		return
	}
	variants, err := LoadScript(string(src))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// 变体名去重校验（同名变体会互相覆盖 CSV）
	seen := map[string]bool{}
	for _, v := range variants {
		if v.Name == "" {
			writeErr(w, http.StatusBadRequest, "存在未命名变体")
			return
		}
		if seen[v.Name] {
			writeErr(w, http.StatusBadRequest, "变体名重复: "+v.Name)
			return
		}
		seen[v.Name] = true
	}
	cfg.ScriptName = scriptName()
	if err := s.runner.Start(cfg, variants); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "variants": len(variants)})
}

// scriptName 脚本文件名（去扩展名）作为报告标识。
func scriptName() string {
	base := filepath.Base(ScriptPath)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// handleStatus 进度查询。
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.runner.Status())
}

// handleStop 停止任务。
func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	s.runner.Stop()
	writeJSON(w, map[string]any{"ok": true})
}

// handleLatestReport 最新完成报告。
func (s *Server) handleLatestReport(w http.ResponseWriter, r *http.Request) {
	rep := s.runner.LatestReport()
	if rep == nil {
		writeErr(w, http.StatusNotFound, "暂无完成的报告")
		return
	}
	writeJSON(w, rep)
}

// handleFactors 因子目录（注册表 All()，供前端下拉）。
func (s *Server) handleFactors(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, f.All())
}

// handleAnalyze 启动因子分析（不加载脚本，与回测共用 Runner 互斥）。
func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	var cfg AnalyzeConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效: "+err.Error())
		return
	}
	cfg.ScriptName = scriptName()
	if err := s.runner.StartAnalysis(cfg); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// handleLatestAnalysis 最新完成的分析报告。内存无最近报告时按无参数合同
// 从全局 latest 指针（含 kind 与 analysisId）确定性恢复，不随机扫描猜测。
func (s *Server) handleLatestAnalysis(w http.ResponseWriter, r *http.Request) {
	rep := s.runner.LatestAnalysis()
	if rep == nil {
		var err error
		rep, err = s.analysisStore.Latest("")
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "恢复最新分析失败")
			return
		}
	}
	if rep == nil {
		writeErr(w, http.StatusNotFound, "暂无完成的分析报告")
		return
	}
	writeJSON(w, rep)
}

// handleAnalyses 分析历史摘要（kind 必填，时间倒序）。
func (s *Server) handleAnalyses(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		writeErr(w, http.StatusBadRequest, "缺少 kind 参数")
		return
	}
	list, err := s.analysisStore.List(kind)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取分析历史失败")
		return
	}
	writeJSON(w, list)
}

// handleAnalysis 指定不可变分析报告。
func (s *Server) handleAnalysis(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("analysisId")
	rep, err := s.analysisStore.Get(id)
	if err != nil {
		if errors.Is(err, errAnalysisNotFound) {
			writeErr(w, http.StatusNotFound, "分析报告不存在: "+id)
			return
		}
		writeErr(w, http.StatusInternalServerError, "读取分析报告失败")
		return
	}
	writeJSON(w, rep)
}

// handleStrategyPresets 简单模式预设策略目录（声明展示，不含 Go 类型）。
func (s *Server) handleStrategyPresets(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, PresetInfos())
}

// handleStrategyRun 简单模式运行：decode → spec 校验 → 构建变体 → 运行范围
// 校验，全部通过后才进入 Runner；请求问题一律 400，仅任务互斥为 409。
// 不读取脚本文件：预设 Buyer 由 presets.go 直接构建。
func (s *Server) handleStrategyRun(w http.ResponseWriter, r *http.Request) {
	var spec StrategySpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效: "+err.Error())
		return
	}
	if err := spec.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	variants, err := spec.Variants()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg := spec.RunConfig()
	if err := cfg.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.runner.StartStrategy(cfg, variants, spec); err != nil {
		// 走到这里说明请求已全部校验通过，剩余错误只有任务互斥
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "variants": len(variants), "source": sourceSimple})
}

// handleReports 历史报告列表（扫描 output/trades/*/report.json）。
func (s *Server) handleReports(w http.ResponseWriter, r *http.Request) {
	type reportItem struct {
		ID     string `json:"id"`
		Script string `json:"script"`
		Time   string `json:"time"`
		Start  string `json:"startYear"`
		End    string `json:"endYear"`
	}
	entries, err := os.ReadDir(filepath.Join("output", "trades"))
	if err != nil {
		writeJSON(w, []reportItem{})
		return
	}
	items := []reportItem(nil)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join("output", "trades", e.Name(), "report.json"))
		if err != nil {
			continue
		}
		var rep Report
		if json.Unmarshal(data, &rep) != nil {
			continue
		}
		items = append(items, reportItem{
			ID:     e.Name(),
			Script: rep.Config.ScriptName,
			Time:   rep.FinishedAt,
			Start:  fmt.Sprintf("%d", rep.Config.StartYear),
			End:    fmt.Sprintf("%d", rep.Config.EndYear),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID > items[j].ID })
	writeJSON(w, items)
}

// handleReport 指定报告（id 为运行目录名）。
func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// 防路径穿越：id 不允许含路径分隔符
	if strings.ContainsAny(id, `/\`) || strings.HasPrefix(id, ".") {
		writeErr(w, http.StatusBadRequest, "非法报告 ID")
		return
	}
	data, err := os.ReadFile(filepath.Join("output", "trades", id, "report.json"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "报告不存在: "+id)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(data)
}

// handleKline 日K 数据（ECharts 格式直出）。
func (s *Server) handleKline(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")

	start := time.Time{}
	if from != "" {
		if t, err := time.Parse(time.DateOnly, from); err == nil {
			start = t
		}
	}
	end := time.Now()
	if to != "" {
		if t, err := time.Parse(time.DateOnly, to); err == nil {
			end = t.Add(23*time.Hour + 59*time.Minute)
		}
	}

	ks, err := commonPullDayKlines(code, start, end)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "拉取日K失败: "+err.Error())
		return
	}
	type klineRow struct {
		Date   string  `json:"date"`
		Open   float64 `json:"open"`
		Close  float64 `json:"close"`
		Low    float64 `json:"low"`
		High   float64 `json:"high"`
		Volume int64   `json:"volume"`
	}
	rows := make([]klineRow, 0, len(ks))
	for _, k := range ks {
		rows = append(rows, klineRow{
			Date:   k.Time.Format(time.DateOnly),
			Open:   k.Open.Float64(),
			Close:  k.Close.Float64(),
			Low:    k.Low.Float64(),
			High:   k.High.Float64(),
			Volume: k.Volume,
		})
	}
	writeJSON(w, rows)
}

// maxCandidateBody 候选请求体上限（64 KiB）。
const maxCandidateBody = 64 << 10

// candidateResponse 候选响应：追加实时兼容状态（不回写历史 JSON）。
type candidateResponse struct {
	FactorCandidate
	Compatibility CandidateCompatibility `json:"compatibility"`
}

// toCandidateResponse 由当前注册表实时计算兼容状态。
func toCandidateResponse(c FactorCandidate) candidateResponse {
	return candidateResponse{FactorCandidate: c, Compatibility: compatibilityOf(c)}
}

// decodeStrict 严格解码请求体：MaxBytesReader 限制大小、拒绝未知字段与多余 JSON。
// 失败时已写错误响应并返回 false。
func decodeStrict(r *http.Request, w http.ResponseWriter, dst any, maxBytes int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeErr(w, http.StatusRequestEntityTooLarge, "请求体过大（上限 64 KiB）")
		} else {
			writeErr(w, http.StatusBadRequest, "请求体无效: "+err.Error())
		}
		return false
	}
	if dec.More() {
		writeErr(w, http.StatusBadRequest, "请求体包含多余内容")
		return false
	}
	return true
}

// handleCreateCandidate 创建候选：读 v3 报告 → 校验因子版本一致 → Store.Create。
// 新建返回 201，幂等命中返回 200；幂等键复用/旧报告/版本漂移返回 409。
func (s *Server) handleCreateCandidate(w http.ResponseWriter, r *http.Request) {
	var req CreateCandidateRequest
	if !decodeStrict(r, w, &req, maxCandidateBody) {
		return
	}
	if !validUUID(req.RequestID) {
		writeErr(w, http.StatusBadRequest, "requestId 必须是合法 UUID")
		return
	}
	if !validAnalysisID(req.AnalysisID) {
		writeErr(w, http.StatusBadRequest, "analysisId 非法")
		return
	}
	rep, err := s.analysisStore.Get(req.AnalysisID)
	if err != nil {
		if errors.Is(err, errAnalysisNotFound) {
			writeErr(w, http.StatusNotFound, "分析报告不存在: "+req.AnalysisID)
			return
		}
		writeErr(w, http.StatusInternalServerError, "读取分析报告失败")
		return
	}
	// 仅 v3 报告可保存（缺 analysisId/实现版本的旧报告拒绝）
	if rep.AnalysisVersion < 3 || !validAnalysisID(rep.AnalysisID) || rep.Factor.ImplementationVersion <= 0 {
		writeErr(w, http.StatusConflict, "该分析为旧版本报告，无法保存为候选，请重新运行分析")
		return
	}
	// 报告因子当前版本仍一致（版本漂移 fail closed）
	entry, ok := f.Catalog(rep.Kind)
	if !ok {
		writeErr(w, http.StatusConflict, "因子类型已从注册表移除")
		return
	}
	if entry.ImplementationVersion != rep.Factor.ImplementationVersion {
		writeErr(w, http.StatusConflict, "因子实现版本已变化，请重新分析后再保存")
		return
	}
	c, created, err := s.candidateStore.Create(req, rep)
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
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]any{"created": created, "candidate": toCandidateResponse(c)})
}

// handleListCandidates 候选列表（默认隐藏归档；includeArchived 只接受 true/false）。
func (s *Server) handleListCandidates(w http.ResponseWriter, r *http.Request) {
	includeArchived := false
	switch r.URL.Query().Get("includeArchived") {
	case "", "false":
	case "true":
		includeArchived = true
	default:
		writeErr(w, http.StatusBadRequest, "includeArchived 只接受 true/false")
		return
	}
	list, err := s.candidateStore.List(includeArchived)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取候选列表失败")
		return
	}
	out := make([]candidateResponse, 0, len(list))
	for _, c := range list {
		out = append(out, toCandidateResponse(c))
	}
	writeJSON(w, map[string]any{"candidates": out})
}

// handleGetCandidate 候选详情（含实时兼容状态）。
func (s *Server) handleGetCandidate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c, err := s.candidateStore.Get(id)
	if err != nil {
		if errors.Is(err, errCandidateNotFound) {
			writeErr(w, http.StatusNotFound, "候选因子不存在")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"candidate": toCandidateResponse(c)})
}

// handleUpdateCandidate 追加候选修订：expectedRevision 冲突 → 409。
func (s *Server) handleUpdateCandidate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req UpdateCandidateRequest
	if !decodeStrict(r, w, &req, maxCandidateBody) {
		return
	}
	c, err := s.candidateStore.Update(id, req)
	if err != nil {
		switch {
		case errors.Is(err, errRevisionConflict):
			writeErr(w, http.StatusConflict, err.Error())
		case errors.Is(err, errCandidateNotFound):
			writeErr(w, http.StatusNotFound, "候选因子不存在")
		default:
			writeErr(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	writeJSON(w, map[string]any{"candidate": toCandidateResponse(c)})
}

// writeJSON 统一 JSON 响应。
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

// writeErr 错误响应。
func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]any{"error": msg})
}

// commonPullDayKlines 日K数据源（委托 common.Pull，与回测循环同源）。
func commonPullDayKlines(code string, start, end time.Time) (extend.Klines, error) {
	return common.Pull.DayKlines(code, start, end)
}
