package lab

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/tdx/v2/protocol"
)

// TestServerRunLifecycle 完整生命周期：
// run → status 轮询到 done → report/latest → 落盘产物 → reports 列表 → report/{id} → kline。
func TestServerRunLifecycle(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// 伪造数据源：sh600000 从 2024-06 起约 250 个交易日恒价上行（每日 +5%，不触涨停过滤）
	// 注入内存行情读取器
	old := common.Pull
	common.Pull = fixturePull(t, dir)
	t.Cleanup(func() { common.Pull = old })
	writeFakeDayDB(t, dir, "sh600000", 250, time.Date(2024, 6, 1, 0, 0, 0, 0, time.Local))

	h := NewServer().Handler()

	// 初始 idle
	res := doReq(t, h, http.MethodGet, "/api/status", nil, http.StatusOK)
	var st map[string]any
	if err := json.Unmarshal(res, &st); err != nil {
		t.Fatal(err)
	}
	if st["state"] != "idle" {
		t.Fatalf("初始状态应为 idle: %v", st)
	}

	// 启动回测（codes 模式单只股票，不触网）
	spec := baseSpec()
	spec.Name = "基线·全买"
	spec.SellSignal = nil
	spec.Risks = []*BuilderNode{bCond("risk_holding_days", map[string]any{"days": 1.0})}
	cfg := map[string]any{
		"startYear": 2025, "endYear": 2025,
		"sampleMode": "codes", "sampleCodes": []string{"sh600000"},
		"builder": spec,
	}
	res = doReq(t, h, http.MethodPost, "/api/builder/run", cfg, http.StatusOK)
	if !bytes.Contains(res, []byte(`"ok":true`)) {
		t.Fatalf("run 响应异常: %s", res)
	}

	// 轮询至完成
	deadline := time.Now().Add(30 * time.Second)
	for {
		res = doReq(t, h, http.MethodGet, "/api/status", nil, http.StatusOK)
		st = nil
		if err := json.Unmarshal(res, &st); err != nil {
			t.Fatal(err)
		}
		if st["state"] == "done" || st["state"] == "error" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("回测超时未完成: %v", st)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st["state"] != "done" {
		t.Fatalf("回测失败: %v", st)
	}
	if n := st["totalCodes"].(float64); n != 1 {
		t.Fatalf("totalCodes=%v", n)
	}

	// 最新报告
	res = doReq(t, h, http.MethodGet, "/api/report/latest", nil, http.StatusOK)
	var rep Report
	if err := json.Unmarshal(res, &rep); err != nil {
		t.Fatal(err)
	}
	// 响应注入报告 ID（= 落盘目录名），前端回放请求以此为定位键（plan T4）。
	var latestRaw map[string]any
	if err := json.Unmarshal(res, &latestRaw); err != nil {
		t.Fatal(err)
	}
	if latestRaw["id"] != rep.RunID() {
		t.Fatalf("latest 响应 id=%v, 期望 %s", latestRaw["id"], rep.RunID())
	}
	if rep.Config.ScriptName != sourceBuilder || rep.Source != sourceBuilder || len(rep.Variants) != 1 {
		t.Fatalf("报告结构异常: script=%q variants=%d", rep.Config.ScriptName, len(rep.Variants))
	}
	if rep.Coverage.Requested != 1 || rep.Coverage.Completed != 1 || rep.Coverage.Skipped != 0 {
		t.Fatalf("数据覆盖异常: %+v", rep.Coverage)
	}
	vr := rep.Variants[0]
	if vr.Name != "基线·全买" || vr.Stats.Total == 0 || len(vr.Trades) != vr.Stats.Total {
		t.Fatalf("变体报告异常: %+v stats=%+v", vr.Name, vr.Stats)
	}
	tr := vr.Trades[0]
	if tr.Code != "sh600000" || tr.Quantity != 100 || tr.BuyPrice <= 0 || tr.BuyTime == "" || tr.SellTime == "" {
		t.Fatalf("交易明细字段异常: %+v", tr)
	}

	// 落盘产物（AGENTS.md 6.1）：report.json + CSV + 汇总 HTML
	runID := rep.RunID()
	if _, err := os.Stat(filepath.Join("output", "trades", runID, "report.json")); err != nil {
		t.Fatalf("report.json 未落盘: %v", err)
	}
	csvs, _ := filepath.Glob(filepath.Join("output", "trades", sourceBuilder, "*.csv"))
	if len(csvs) == 0 {
		t.Fatal("变体 CSV 未落盘")
	}
	htmls, _ := filepath.Glob(filepath.Join("output", "trades", sourceBuilder, "*_summary.html"))
	if len(htmls) == 0 {
		t.Fatal("汇总 HTML 未落盘")
	}

	// 历史列表 + 指定报告
	res = doReq(t, h, http.MethodGet, "/api/reports", nil, http.StatusOK)
	var items []map[string]any
	if err := json.Unmarshal(res, &items); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range items {
		if it["id"] == runID {
			found = true
		}
	}
	if !found {
		t.Fatalf("reports 列表缺少 %s: %s", runID, res)
	}
	res = doReq(t, h, http.MethodGet, "/api/report/"+runID, nil, http.StatusOK)
	if !bytes.Contains(res, []byte(`"基线·全买"`)) {
		t.Fatalf("report/{id} 内容异常: %s", res)
	}
	var repRaw map[string]any
	if err := json.Unmarshal(res, &repRaw); err != nil {
		t.Fatal(err)
	}
	if repRaw["id"] != runID {
		t.Fatalf("report/{id} 响应 id=%v, 期望 %s", repRaw["id"], runID)
	}

	// 路径穿越防护
	doReq(t, h, http.MethodGet, "/api/report/a%2Fb", nil, http.StatusBadRequest)

	// 日K接口（读伪造数据）
	res = doReq(t, h, http.MethodGet, "/api/kline/sh600000?from=2025-01-01&to=2025-12-31", nil, http.StatusOK)
	var rows []map[string]any
	if err := json.Unmarshal(res, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("kline 接口无数据")
	}
	if d, _ := rows[0]["date"].(string); len(d) != 10 {
		t.Fatalf("kline date 格式异常: %v", rows[0]["date"])
	}

	// stop 空操作
	doReq(t, h, http.MethodPost, "/api/stop", nil, http.StatusOK)
}

// TestServerAnalyzeYearRange 因子分析 API 契约：请求年份决定报告区间，非法年份被拒绝，
// 最新报告携带配置区间、实际有效日期与逐年覆盖率。
func TestServerAnalyzeYearRange(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	old := common.Pull
	common.Pull = fixturePull(t, dir)
	t.Cleanup(func() { common.Pull = old })

	now := time.Now().Year()
	base := time.Date(now-1, 12, 24, 0, 0, 0, 0, time.Local)
	codes := []string{"sh600001", "sh600002", "sh600003", "sh600004", "sh600005"}
	for i, step := range []float64{0.6, 0.1, 0, -0.12, -0.35} {
		closes := make([]float64, 28)
		for j := range closes {
			closes[j] = 10 + step*float64(j)
		}
		writeDayDBCloses(t, dir, codes[i], closes, base)
	}

	h := NewServer().Handler()
	// 非法年份：后端校验是最终约束，不依赖前端输入限制
	bad := map[string]any{"startYear": now, "endYear": now + 1, "sampleMode": "codes",
		"sampleCodes": codes, "kind": "momentum", "days": 2, "window": 1}
	doReq(t, h, http.MethodPost, "/api/analyze", bad, http.StatusConflict)
	bad["startYear"], bad["endYear"] = now, now-1
	doReq(t, h, http.MethodPost, "/api/analyze", bad, http.StatusConflict)

	// 合法区间：now-2 无数据，now-1 与 now 参与分析
	cfg := map[string]any{"startYear": now - 2, "endYear": now, "sampleMode": "codes",
		"sampleCodes": codes, "kind": "momentum", "days": 2, "window": 1}
	res := doReq(t, h, http.MethodPost, "/api/analyze", cfg, http.StatusOK)
	if !bytes.Contains(res, []byte(`"ok":true`)) {
		t.Fatalf("analyze 响应异常: %s", res)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		res = doReq(t, h, http.MethodGet, "/api/status", nil, http.StatusOK)
		var st map[string]any
		if err := json.Unmarshal(res, &st); err != nil {
			t.Fatal(err)
		}
		if st["state"] == "done" || st["state"] == "error" {
			if st["state"] != "done" {
				t.Fatalf("分析失败: %v", st)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("分析超时未完成: %v", st)
		}
		time.Sleep(50 * time.Millisecond)
	}

	res = doReq(t, h, http.MethodGet, "/api/analysis/latest", nil, http.StatusOK)
	var rep AnalysisReport
	if err := json.Unmarshal(res, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Range.StartYear != now-2 || rep.Range.EndYear != now ||
		rep.Range.SampleMode != "codes" || rep.Range.SampleSize != len(codes) {
		t.Fatalf("Range = %+v", rep.Range)
	}
	if len(rep.Years) != 2 || rep.Years[0].Year != now-1 || rep.Years[1].Year != now {
		t.Fatalf("Years = %+v", rep.Years)
	}
	if rep.FirstDataDate == "" || rep.LastDataDate == "" {
		t.Fatalf("实际有效日期缺失: %q–%q", rep.FirstDataDate, rep.LastDataDate)
	}
	if rep.YearCoverage.RequestedCodeYears != len(codes)*3 ||
		rep.YearCoverage.CompletedCodeYears != len(codes)*2 ||
		rep.YearCoverage.SkippedCodeYears != len(codes) {
		t.Fatalf("YearCoverage = %+v", rep.YearCoverage)
	}
}

// TestRunnerMutualExclusion 已有任务运行时 Start 报错（单任务互斥）。
func TestRunnerMutualExclusion(t *testing.T) {
	r := NewRunner()
	r.mu.Lock()
	defer r.mu.Unlock()
	err := r.Start(RunConfig{
		StartYear: 2025, EndYear: 2025,
		SampleMode: "codes", SampleCodes: []string{"sh600000"},
		HoldingDays: 1,
	}, []core.Variant{{Name: "x", Buyer: sb.A价格{Max: 120}}})
	if err == nil {
		t.Fatal("互斥未生效")
	}
}

// TestRunConfigValidate 配置校验边界。
func TestRunConfigValidate(t *testing.T) {
	base := RunConfig{
		StartYear: 2024, EndYear: 2025,
		SampleMode: "codes", SampleCodes: []string{"sh600000"},
		HoldingDays: 1,
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("合法配置不应报错: %v", err)
	}

	cases := map[string]func(*RunConfig){
		"年份倒置":   func(c *RunConfig) { c.StartYear, c.EndYear = 2026, 2025 },
		"未来年份":   func(c *RunConfig) { c.EndYear = time.Now().Year() + 1 },
		"样本模式无效": func(c *RunConfig) { c.SampleMode = "xxx" },
		"随机样本为0": func(c *RunConfig) { c.SampleMode = "random"; c.SampleSize = 0 },
		"指定代码为空": func(c *RunConfig) { c.SampleCodes = nil },
		"卖出规则全空": func(c *RunConfig) { c.HoldingDays = 0 },
	}
	for name, mutate := range cases {
		c := base
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s 未报错", name)
		}
	}
}

// TestServerStrategyPresets 简单模式预设目录：五项、顺序稳定、展示字段完整。
func TestServerStrategyPresets(t *testing.T) {
	h := NewServer().Handler()
	res := doReq(t, h, http.MethodGet, "/api/strategy-presets", nil, http.StatusOK)
	var items []PresetInfo
	if err := json.Unmarshal(res, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 5 {
		t.Fatalf("预设数 = %d, want 5", len(items))
	}
	wantIDs := []string{"pullback_ma5_up", "pullback_ma10_up", "pullback_ma5_bull", "pullback_ma5_plain", "macd_bar_up"}
	for i, it := range items {
		if it.ID != wantIDs[i] {
			t.Fatalf("预设[%d].id = %q, want %q", i, it.ID, wantIDs[i])
		}
		if it.Name == "" || it.Description == "" || len(it.Rules) == 0 {
			t.Fatalf("预设[%d] 展示字段不完整: %+v", i, it)
		}
	}
}

// simpleRunBody 合法简单模式请求体（2 个条件），供校验/忙碌/生命周期测试变异。
func simpleRunBody() map[string]any {
	return map[string]any{
		"version":      1,
		"basePresetId": "pullback_ma5_plain",
		"factorFilters": []map[string]any{
			{"kind": "kvalue", "days": 9, "operator": "between", "min": 0, "max": 100},
			{"kind": "position", "days": 60, "operator": "between", "min": 0, "max": 100},
		},
		"exit": map[string]any{"holdingDays": 1},
		"run": map[string]any{
			"startYear": 2025, "endYear": 2025,
			"sampleMode": "codes", "sampleCodes": []string{"sh600000"},
		},
	}
}

// TestServerStrategyRunValidation 请求问题一律 400：非法 JSON、版本/未知预设/
// 重复/操作符/区间错误；错误体含 error 字段。条件数量不限（0 与 >4 合法），
// 不再是校验项。
func TestServerStrategyRunValidation(t *testing.T) {
	h := NewServer().Handler()

	// 非法 JSON → 400（body 无法经 json.Marshal 构造，直接发原始字节）
	req := httptest.NewRequest(http.MethodPost, "/api/strategy/run", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法 JSON 状态码 = %d, want 400，响应: %s", rec.Code, rec.Body.String())
	}

	cases := map[string]func(map[string]any){
		"版本无效": func(b map[string]any) { b["version"] = 2 },
		"未知预设": func(b map[string]any) { b["basePresetId"] = "nope" },
		"重复条件": func(b map[string]any) {
			fs := b["factorFilters"].([]map[string]any)
			b["factorFilters"] = []map[string]any{fs[0], fs[0]}
		},
		"操作符无效": func(b map[string]any) { b["factorFilters"].([]map[string]any)[0]["operator"] = "gt" },
		"区间反向": func(b map[string]any) {
			fs := b["factorFilters"].([]map[string]any)
			fs[0]["min"], fs[0]["max"] = 100, 0
		},
		"运行年份缺失": func(b map[string]any) {
			run := b["run"].(map[string]any)
			delete(run, "startYear")
		},
	}
	for name, mutate := range cases {
		body := simpleRunBody()
		mutate(body)
		res := doReq(t, h, http.MethodPost, "/api/strategy/run", body, http.StatusBadRequest)
		if !strings.Contains(string(res), "error") {
			t.Fatalf("%s 响应缺少 error 字段: %s", name, res)
		}
	}
}

// TestServerStrategyRunBusy 任务互斥期间请求返回 409，且不覆盖正在运行的任务。
func TestServerStrategyRunBusy(t *testing.T) {
	s := NewServer()
	s.runner.mu.Lock() // 模拟已有任务占用（同 TestRunnerMutualExclusion）
	defer s.runner.mu.Unlock()
	doReq(t, s.Handler(), http.MethodPost, "/api/strategy/run", simpleRunBody(), http.StatusConflict)
	standalone := simpleRunBody()
	standalone["basePresetId"] = ""
	doReq(t, s.Handler(), http.MethodPost, "/api/strategy/run", standalone, http.StatusConflict)
	if s.runner.LatestReport() != nil {
		t.Fatal("忙碌时不应产生新报告")
	}
}

// TestServerStopSameOrigin /api/stop 属于写操作，必须过同源检查：
// 跨站来源（Origin host/scheme 不符或 Sec-Fetch-Site=cross-site）一律 403
// 拒绝，不得关闭任务通道；同源请求正常放行（无任务运行也返回 200）。
func TestServerStopSameOrigin(t *testing.T) {
	handler := NewServer().Handler()

	newStopReq := func(headers map[string]string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/api/stop", nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		return req
	}
	doStop := func(headers map[string]string) int {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, newStopReq(headers))
		return rec.Code
	}

	// 跨站 Origin：拒绝。
	for _, tc := range []struct{ name, origin string }{
		{"跨站host", "http://evil.example.com"},
		{"跨站scheme", "https://example.com"},
		{"跨站端口", "http://example.com:9999"},
	} {
		if code := doStop(map[string]string{"Origin": tc.origin}); code != http.StatusForbidden {
			t.Fatalf("%s: Origin=%q 应拒绝 403，实际 %d", tc.name, tc.origin, code)
		}
	}
	// 跨站 Sec-Fetch-Site：拒绝。
	if code := doStop(map[string]string{"Sec-Fetch-Site": "cross-site"}); code != http.StatusForbidden {
		t.Fatalf("Sec-Fetch-Site=cross-site 应拒绝 403，实际 %d", code)
	}
	// 同源：放行（无任务运行也返回 200）。
	if code := doStop(map[string]string{"Origin": "http://example.com"}); code != http.StatusOK {
		t.Fatalf("同源 Origin 应放行 200，实际 %d", code)
	}
}

// writeFakeDayDB 为内存行情填充日线。
// 每天开10收10.5（+5%，不触发涨停过滤），恒价保证买入条件每日为真。
func writeFakeDayDB(t *testing.T, dir, code string, days int, base time.Time) {
	t.Helper()
	rows := make([]*extend.Kline, 0, days)
	for i := 0; i < days; i++ {
		tm := base.AddDate(0, 0, i)
		rows = append(rows, &extend.Kline{
			Unix: tm.Unix(),
			Kline: &protocol.Kline{
				Time:   tm,
				Open:   10,
				Close:  10.5,
				High:   10.6,
				Low:    9.9,
				Volume: 10000,
			},
		})
	}
	seedTestDays(t, dir, code, rows)
}

// doReq httptest 请求辅助：断言状态码，返回响应体。
func doReq(t *testing.T, h http.Handler, method, path string, body any, wantCode int) []byte {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != wantCode {
		t.Fatalf("%s %s: 状态码 %d (期望 %d)，响应: %s", method, path, rec.Code, wantCode, rec.Body.String())
	}
	return rec.Body.Bytes()
}
