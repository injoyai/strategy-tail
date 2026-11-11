package lab

import (
	"embed"
	"encoding/json"
	"errors"

	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/internal/chartui"
	"github.com/injoyai/strategy-tail/lib/extend"
	f "github.com/injoyai/strategy-tail/strategies/factor"
)

// server.go REST API + 静态页服务。
//
// API 契约见设计文档 §5：
//
//	GET      /api/status        进度查询
//	POST     /api/stop          停止任务
//	GET      /api/report/latest 最新报告
//	GET      /api/reports       历史报告列表
//	GET      /api/report/{id}   指定报告
//	GET      /api/report/{id}/export.xlsx 完整报告 Excel（按需生成）
//	GET      /api/report/{id}/replay/{variant}     变体回放明细（不可用时 400 提示重新回测）
//	GET      /api/report/{id}/replay/kline/{code}  报告内个股 K 线快照（保存时旧快照）
//	GET      /api/kline/{code}  日K数据
//	GET      /api/factors       因子目录
//	POST     /api/analyze       启动因子分析（与回测共用任务互斥）
//	GET      /api/analysis/latest 最新分析报告（内存无报告时从磁盘 latest 指针恢复）
//	GET      /api/analyses?kind=  分析历史摘要（时间倒序）
//	GET      /api/analysis/{analysisId} 指定不可变分析报告
//	GET      /api/strategy-presets 简单模式预设策略目录
//	POST     /api/strategy/run  启动简单模式回测（声明式 StrategySpec；请求问题 400，任务互斥 409）
//	POST /api/factor-candidates 创建候选（201 新建 / 200 幂等命中 / 409 冲突）
//	GET      /api/factor-candidates?includeArchived=  候选列表（默认隐藏归档）
//	GET      /api/factor-candidates/{id} 候选详情（含实时兼容状态）
//	PUT      /api/factor-candidates/{id} 追加候选修订（expectedRevision 冲突 409）
//	GET      /api/builder/components 搭建器组件目录（可运行 + 不可用及原因）
//	GET/POST /api/builder/strategies 策略库列表 / 新建（201）
//	GET/PUT  /api/builder/strategies/{id} 读取 / 覆盖保存（expectedVersion 冲突 409）
//	POST     /api/builder/run 启动搭建器回测（请求问题 400，任务互斥 409）

// Server lab 服务。
type Server struct {
	runner *Runner
	mux    *http.ServeMux
	// analysisStore 不可变分析历史（生产默认 output/factor；测试注入临时根）。
	analysisStore *AnalysisStore
	// candidateStore 候选因子追加式库（生产默认 data/lab/factor-candidates；测试注入临时根）。
	candidateStore *CandidateStore
	// builderStore 搭建器策略库（生产默认 data/lab/strategies；测试注入临时根）。
	builderStore *BuilderStore
	// practiceStore 盘感练习记录（生产默认 data/lab/practice；测试注入临时根）。
	practiceStore *PracticeStore
	trainingStore *TrainingStore

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
		builderStore:   NewBuilderStore(builderDataDir),
		practiceStore:  NewPracticeStore(practiceDataDir),
	}
	s.wirePortfolioStores()
	// 恢复重启前中断的练习准备（标为 failed，可重练出题）。
	if err := s.practiceStore.RecoverInterrupted(); err != nil {
		log.Printf("[lab] 恢复中断的练习会话失败: %v", err)
	}
	s.wirePracticeStores()
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
		builderStore:   NewBuilderStore(builderDataDir),                              // 搭建器测试按需覆盖
		practiceStore:  NewPracticeStore(filepath.Join(candidates.root, "practice")), // 练习测试按需覆盖
	}
	s.wirePortfolioStores()
	s.wirePracticeStores()
	s.registerRoutes()
	return s
}

// wirePracticeStores 注入盘感练习依赖（策略库与练习存储）。
func (s *Server) wirePracticeStores() {
	s.runner.SetPracticeStores(s.practiceStore, s.builderStore)
	if s.trainingStore == nil || s.trainingStore.root != filepath.Join(s.practiceStore.root, "training-v1") {
		s.trainingStore = NewTrainingStore(filepath.Join(s.practiceStore.root, "training-v1"))
		if err := s.trainingStore.recoverBanks(); err != nil {
			log.Printf("[lab] 题库中断恢复失败: %v", err)
		}
	}
	s.runner.trainingStore = s.trainingStore
}

// registerRoutes 注册全部 REST 路由。
//
// 写端点通过 withSameOrigin 或 handler 内的同源校验防止跨站写入。
func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /api/market-data/status", s.handleMarketDataStatus)
	s.mux.HandleFunc("GET /api/status", s.handleStatus)
	s.mux.HandleFunc("POST /api/stop", withSameOrigin(s.handleStop))
	s.mux.HandleFunc("GET /api/report/latest", s.handleLatestReport)
	s.mux.HandleFunc("GET /api/reports", s.handleReports)
	s.mux.HandleFunc("GET /api/report/{id}", s.handleReport)
	s.mux.HandleFunc("GET /api/report/{id}/export.xlsx", s.handleReportExcel)
	s.mux.HandleFunc("DELETE /api/report/{id}", s.handleDeleteReport)
	s.mux.HandleFunc("GET /api/report/{id}/replay/{variant}", s.handleReplayVariant)
	s.mux.HandleFunc("GET /api/report/{id}/replay/kline/{code}", s.handleReplayKline)
	s.mux.HandleFunc("GET /api/kline/{code}", s.handleKline)
	s.mux.HandleFunc("GET /api/stock-names", s.handleStockNames)
	s.mux.HandleFunc("GET /api/stock-info/{code}", s.handleStockInfo)
	s.mux.HandleFunc("GET /api/factors", s.handleFactors)
	s.mux.HandleFunc("POST /api/analyze", withSameOrigin(s.handleAnalyze))
	s.mux.HandleFunc("GET /api/analysis/latest", s.handleLatestAnalysis)
	s.mux.HandleFunc("GET /api/analyses", s.handleAnalyses)
	s.mux.HandleFunc("GET /api/analysis/{analysisId}", s.handleAnalysis)
	s.mux.HandleFunc("GET /api/strategy-presets", s.handleStrategyPresets)
	s.mux.HandleFunc("POST /api/strategy/run", withSameOrigin(s.handleStrategyRun))
	s.mux.HandleFunc("POST /api/factor-candidates", withSameOrigin(s.handleCreateCandidate))
	s.mux.HandleFunc("GET /api/factor-candidates", s.handleListCandidates)
	s.mux.HandleFunc("GET /api/factor-candidates/{id}", s.handleGetCandidate)
	s.mux.HandleFunc("PUT /api/factor-candidates/{id}", withSameOrigin(s.handleUpdateCandidate))

	// 搭建器：自由组合策略（设计文档 §5）。写端点已在 handler 内校验同源。
	s.mux.HandleFunc("GET /api/builder/components", s.handleBuilderComponents)
	s.mux.HandleFunc("GET /api/builder/strategies", s.handleBuilderListStrategies)
	s.mux.HandleFunc("POST /api/builder/strategies", s.handleBuilderCreateStrategy)
	s.mux.HandleFunc("GET /api/builder/strategies/{id}", s.handleBuilderGetStrategy)
	s.mux.HandleFunc("PUT /api/builder/strategies/{id}", s.handleBuilderUpdateStrategy)
	s.mux.HandleFunc("POST /api/builder/run", s.handleBuilderRun)

	// 盘感练习（实施文档 §4）。写端点已在 handler 内校验同源。
	s.registerTrainingRoutes()
	s.mux.HandleFunc("POST /api/practice", s.handlePracticeStart)
	s.mux.HandleFunc("GET /api/practices", s.handlePracticeList)
	s.mux.HandleFunc("GET /api/practice/{id}", s.handlePracticeGet)
	s.mux.HandleFunc("POST /api/practice/{id}/actions", s.handlePracticeAction)
	s.mux.HandleFunc("GET /api/practice/{id}/review", s.handlePracticeReview)
	s.mux.HandleFunc("POST /api/practice/{id}/retry", s.handlePracticeRetry)

	// 多因子组合研究 v2（Task 9，设计 §12.2）。
	s.mux.HandleFunc("POST /api/factor-models", withSameOrigin(s.handleCreateFactorModel))
	s.mux.HandleFunc("GET /api/factor-models", s.handleListFactorModels)
	s.mux.HandleFunc("GET /api/factor-models/{modelId}", s.handleGetFactorModel)
	s.mux.HandleFunc("POST /api/portfolio-experiments", withSameOrigin(s.handleCreateExperiment))
	s.mux.HandleFunc("GET /api/portfolio-experiments", s.handleListExperiments)
	s.mux.HandleFunc("GET /api/portfolio-experiments/{id}", s.handleGetExperiment)
	s.mux.HandleFunc("GET /api/portfolio-experiments/{id}/artifacts/{name}", s.handleExperimentArtifact)
	s.mux.HandleFunc("POST /api/portfolio-validations", withSameOrigin(s.handleCreateValidation))
	s.mux.HandleFunc("GET /api/portfolio-validations", s.handleListValidations)
	s.mux.HandleFunc("GET /api/portfolio-validations/{id}", s.handleGetValidation)
	s.mux.HandleFunc("POST /api/portfolio-runs/{id}/start", withSameOrigin(s.handleStartPortfolioRun))
	s.mux.HandleFunc("POST /api/portfolio-runs/{id}/stop", withSameOrigin(s.handleStopPortfolioRun))
	s.mux.HandleFunc("GET /api/portfolio-runs/{id}", s.handleGetPortfolioRun)
	s.mux.Handle("GET /assets/kline/", http.StripPrefix("/assets/kline/", chartui.Handler()))
	s.mux.HandleFunc("GET /", s.handleIndex)
}

// withSameOrigin 非 GET 写端点的同源校验包装：无鉴权的本地 Lab 防跨站
// 触发写操作（CSRF），规则复用 requireSameOrigin（Origin 与 Host 一致、
// 拒绝 Sec-Fetch-Site: cross-site）。
func withSameOrigin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if requireSameOrigin(w, r) {
			next(w, r)
		}
	}
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
		items, err := listReports()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "读取历史报告失败")
			return
		}
		if len(items) == 0 {
			writeErr(w, http.StatusNotFound, "暂无完成的报告")
			return
		}
		rep, err = readReport(items[0].ID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "读取最新报告失败")
			return
		}
	}
	// 注入报告 ID（目录名，= rep.RunID()）；前端回放请求以此为定位键（plan T3/T4）。
	writeJSON(w, struct {
		*Report
		ID string `json:"id"`
	}{rep, rep.RunID()})
}

// handleFactors 因子目录（注册表 All()，供前端下拉）。
func (s *Server) handleFactors(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, f.All())
}

// handleAnalyze 启动因子分析，与回测共用 Runner 互斥。
func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	var cfg AnalyzeConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效: "+err.Error())
		return
	}
	cfg.ScriptName = "因子分析"
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
	// 账户模式接入原因记录（R07/R15）：构建失败或长度不一致时保持原变体。
	if cfg.Account != nil {
		if recorded, err := spec.RecordVariants(); err == nil && len(recorded) == len(variants) {
			variants = recorded
		}
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
	items, err := listReports()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取历史报告失败")
		return
	}
	writeJSON(w, items)
}

// handleReport 指定报告（id 为运行目录名）。
func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validReportID(id) {
		writeErr(w, http.StatusBadRequest, "非法报告 ID")
		return
	}
	rep, err := readReport(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeErr(w, http.StatusNotFound, "报告不存在")
		} else {
			writeErr(w, http.StatusInternalServerError, "读取报告失败")
		}
		return
	}
	// 注入报告 ID（目录名，= rep.RunID()）；前端回放请求以此为定位键（plan T3/T4）。
	writeJSON(w, struct {
		*Report
		ID string `json:"id"`
	}{rep, rep.RunID()})
}

// handleReplayVariant 变体回放明细（plan §5）。R13：旧报告（replay 描述为
// nil）或未记录变体（Available=false）一律 400 提示重新回测；版本不受
// 支持时透传 readReplay 的用户文案。
func (s *Server) handleReplayVariant(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validReportID(id) {
		writeErr(w, http.StatusBadRequest, "非法报告 ID")
		return
	}
	idx, err := strconv.Atoi(r.PathValue("variant"))
	if err != nil || idx < 0 {
		writeErr(w, http.StatusBadRequest, "非法变体序号")
		return
	}
	rep, err := readReport(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeErr(w, http.StatusNotFound, "报告不存在")
		} else {
			writeErr(w, http.StatusInternalServerError, "读取报告失败")
		}
		return
	}
	if idx >= len(rep.Variants) {
		writeErr(w, http.StatusBadRequest, "变体序号超出范围")
		return
	}
	meta := rep.Variants[idx].Replay
	if meta == nil || !meta.Available {
		writeErr(w, http.StatusBadRequest, "该报告未包含回放数据，请重新回测")
		return
	}
	rv, err := readReplay(id, idx)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeErr(w, http.StatusNotFound, "回放数据不存在")
			return
		}
		var ve *replayVersionError
		if errors.As(err, &ve) {
			writeErr(w, http.StatusInternalServerError, err.Error())
		} else {
			writeErr(w, http.StatusInternalServerError, "读取回放数据失败")
		}
		return
	}
	writeJSON(w, rv)
}

// handleReplayKline 报告目录内的个股 K 线快照（回放图表专用，不实时拉取，
// 行情更新不影响历史报告图表）。只保存实际买入过的股票，其余 404。
func (s *Server) handleReplayKline(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	code := r.PathValue("code")
	if !validReportID(id) {
		writeErr(w, http.StatusBadRequest, "非法报告 ID")
		return
	}
	if !validReplayCode(code) {
		writeErr(w, http.StatusBadRequest, "非法股票代码")
		return
	}
	if _, err := readReport(id); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeErr(w, http.StatusNotFound, "报告不存在")
		} else {
			writeErr(w, http.StatusInternalServerError, "读取报告失败")
		}
		return
	}
	rows, err := readReplayKline(id, code)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeErr(w, http.StatusNotFound, "该股票不在回放数据中")
		} else {
			writeErr(w, http.StatusInternalServerError, "读取K线快照失败")
		}
		return
	}
	writeJSON(w, rows)
}

// klineRow 日K接口与回放快照共用的行情行格式（MACD 默认 12/26/9）。
type klineRow struct {
	Date   string  `json:"date"`
	Open   float64 `json:"open"`
	Close  float64 `json:"close"`
	Low    float64 `json:"low"`
	High   float64 `json:"high"`
	Volume int64   `json:"volume"`
	DIF    float64 `json:"dif"`
	DEA    float64 `json:"dea"`
	MACD   float64 `json:"macd"`
}

// klineSeries 在完整已加载区间（含前导历史）上计算 MACD 因子并逐行构造，
// 时间过滤由调用方按需进行；保证因果指标语义与全量计算一致。
func klineSeries(ks extend.Klines) []klineRow {
	hist := (f.MACD柱值{}).Series(ks)
	dif := (f.MACDDIF值{}).Series(ks)
	rows := make([]klineRow, 0, len(ks))
	for i, k := range ks {
		rows = append(rows, klineRow{
			Date:   k.Time.Format(time.DateOnly),
			Open:   k.Open,
			Close:  k.Close,
			Low:    k.Low,
			High:   k.High,
			Volume: k.Volume,
			DIF:    dif[i], DEA: dif[i] - hist[i], MACD: hist[i],
		})
	}
	return rows
}

// handleKline 日K 数据（绘图库无关的行情行格式）。
func (s *Server) handleKline(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")

	start := time.Time{}
	if from != "" {
		if t, err := time.ParseInLocation(time.DateOnly, from, time.Local); err == nil {
			start = t
		}
	}
	end := time.Now()
	if to != "" {
		if t, err := time.ParseInLocation(time.DateOnly, to, time.Local); err == nil {
			end = t.Add(23*time.Hour + 59*time.Minute)
		}
	}

	ks, err := commonPullDayKlines(code, time.Time{}, end)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "拉取日K失败: "+err.Error())
		return
	}
	all := klineSeries(ks)
	rows := make([]klineRow, 0, len(all))
	for i, k := range ks {
		if k.Time.Before(start) || k.Time.After(end) {
			continue
		}
		rows = append(rows, all[i])
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
