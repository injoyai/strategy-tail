package lab

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
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
//	GET      /api/analysis/latest 最新分析报告
//	GET      /api/strategy-presets 简单模式预设策略目录
//	POST     /api/strategy/run  启动简单模式回测（声明式 StrategySpec；请求问题 400，任务互斥 409）

// ScriptPath 策略脚本路径（页面编辑器直接读写该文件）。
const ScriptPath = "strategies/script/matrix.go"

// Server lab 服务。
type Server struct {
	runner *Runner
	mux    *http.ServeMux
}

// NewServer 创建服务并注册路由。
func NewServer() *Server {
	s := &Server{runner: NewRunner(), mux: http.NewServeMux()}
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
	s.mux.HandleFunc("GET /api/strategy-presets", s.handleStrategyPresets)
	s.mux.HandleFunc("POST /api/strategy/run", s.handleStrategyRun)
	s.mux.HandleFunc("GET /", s.handleIndex)
	return s
}

// Handler 返回 http.Handler（测试用 httptest 直连）。
func (s *Server) Handler() http.Handler { return s.mux }

// webFS 前端静态资源（go:embed，无构建链）。
//
//go:embed web
var webFS embed.FS

// handleIndex 静态页服务（web/lab/index.html）。
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := webFS.ReadFile("web/lab/index.html")
	if err != nil {
		http.Error(w, "前端页面缺失: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
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

// handleLatestAnalysis 最新完成的分析报告。
func (s *Server) handleLatestAnalysis(w http.ResponseWriter, r *http.Request) {
	rep := s.runner.LatestAnalysis()
	if rep == nil {
		writeErr(w, http.StatusNotFound, "暂无完成的分析报告")
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
