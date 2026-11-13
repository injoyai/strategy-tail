package lab

import (
	"context"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/screener"
	"github.com/injoyai/tdx/v2/protocol"
)

var screenStockCode = regexp.MustCompile(`^(sh|sz|bj)?[0-9]{6}$`)

type screenDiagnosis struct {
	Code        string                `json:"code"`
	Name        string                `json:"name"`
	AsOf        string                `json:"asOf"`
	Klines      []klineRow            `json:"klines"`
	Signals     []screener.Signal     `json:"signals"`
	Rules       []core.DiagnoseResult `json:"rules"`
	Explain     []core.ExplainStep    `json:"explain,omitempty"`
	Annotations []core.Annotation     `json:"annotations,omitempty"`
	Warning     string                `json:"warning,omitempty"`
}

func (s *Server) handleScreenDiagnose(w http.ResponseWriter, r *http.Request) {
	code := strings.ToLower(r.PathValue("code"))
	if !screenStockCode.MatchString(code) {
		writeErr(w, http.StatusBadRequest, "请输入有效的 A 股代码")
		return
	}
	code = protocol.AddPrefix(code)
	if !protocol.IsStock(code) {
		writeErr(w, http.StatusBadRequest, "请输入有效的 A 股代码")
		return
	}
	screen := s.screen
	signals, config, history, err := screen.historySnapshot(false)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "无法读取选股配置")
		return
	}
	at := screen.now().In(protocol.Location())
	end := at
	if at.Hour() < 15 {
		end = at.AddDate(0, 0, -1)
	}
	bars, err := screen.market.DayKlines(code, time.Time{}, end)
	if err != nil {
		log.Printf("[screen] 读取诊断日线失败: %v", err)
		writeErr(w, http.StatusServiceUnavailable, "无法读取股票日线")
		return
	}
	if len(bars) == 0 {
		writeErr(w, http.StatusNotFound, "该股票暂无日线")
		return
	}
	out := screenDiagnosis{Code: code, Name: screen.market.Name(code), Signals: []screener.Signal{}, Rules: []core.DiagnoseResult{}}
	minutes, err := screen.market.StockSessionBars([]string{code}, at)
	session := minutes[code]
	if err != nil {
		out.Warning = "盘中行情读取失败，已显示最近可用日线。"
	}
	if session == nil && bars[len(bars)-1].Time.Format(time.DateOnly) < at.Format(time.DateOnly) {
		screen.mu.Lock()
		read := screen.liveMinutes
		screen.mu.Unlock()
		if read != nil && at.Weekday() != time.Saturday && at.Weekday() != time.Sunday && at.Hour() >= 9 {
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			live, err := read(ctx, code, at)
			cancel()
			if err == nil && len(live) > 0 {
				session = live.Kline(live[len(live)-1].Time, bars[len(bars)-1].Close)
				out.Warning = ""
			}
		}
	}
	bars = screener.WithSessionBar(screen.market, code, at, bars, session)
	out.Klines = klineSeries(bars)
	out.AsOf = bars[len(bars)-1].Time.Format(time.DateTime)
	selected := r.URL.Query().Get("strategy")
	for _, row := range signals {
		if row.Code == code && (selected == "" || row.Strategy == selected) {
			out.Signals = append(out.Signals, row)
		}
	}

	// Include the current intraday candidate as a marker even before it
	// becomes a historical closing signal.
	screen.mu.Lock()
	if screen.version == config.Version && screen.result != nil {
		for _, row := range screen.result.Matches {
			if row.Code != code {
				continue
			}
			for _, hit := range row.Hits {
				if selected == "" || hit.ID == selected {
					if signalMissing(out.Signals, hit.ID, row.Time[:10]) {
						out.Signals = append(out.Signals, screener.Signal{Code: code, Name: out.Name, Strategy: hit.ID,
							BuyTime: row.Time, BuyPrice: row.Price, LastPrice: row.Price, Tags: hit.Tags})
					}
				}
			}
			break
		}
	}
	screen.mu.Unlock()

	if history.Running {
		out.Warning = strings.TrimSpace(out.Warning + " 历史信号正在计算，标记可能尚未齐全。")
	}
	if history.ArchiveError != "" {
		out.Warning = strings.TrimSpace(out.Warning + " 部分历史信号暂不可用。")
	}

	for i := range config.Strategies {
		st := &config.Strategies[i]
		st.Enabled = st.ID == selected || selected == "" && st.Enabled
	}
	presets, custom, err := compileScreen(config)
	if err == nil {
		var rules []screener.Rule
		rules, err = screener.BuildRules(screen.market, at, end, presets, custom)
		if err == nil {
			for _, rule := range rules {
				input := rule.Input(bars)
				_, diagnosis := core.Diagnose(rule.Buyer, code, input)
				diagnosis.Name = rule.Name
				out.Rules = append(out.Rules, diagnosis)
				if selected != "" {
					if visual, ok := rule.Buyer.(core.Visualizer); ok {
						out.Annotations = visual.Annotate(code, input)
					}
					if explainer, ok := rule.Buyer.(core.Explainer); ok {
						out.Explain = explainer.Explain(code, input)
					}
				}
			}
		}
	}
	if err != nil {
		log.Printf("[screen] 计算诊断条件失败: %v", err)
		out.Warning = strings.TrimSpace(out.Warning + " 策略条件诊断暂不可用。")
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, out)
}

func signalMissing(rows []screener.Signal, strategy, date string) bool {
	for _, row := range rows {
		if row.Strategy == strategy && strings.HasPrefix(row.BuyTime, date) {
			return false
		}
	}
	return true
}
