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
	"github.com/injoyai/tdx/lib/xorms"
	"github.com/injoyai/tdx/protocol"
)

// server_test.go 按 httptest 走 API 契约（设计文档 §8）：
// 脚本读写、check 报错、run→status→report 生命周期。
//
// 数据隔离：chdir 到临时目录（ScriptPath 与 output/ 均为相对路径），
// 并将 common.Pull 临时替换为指向伪造日K sqlite 的实例（MinKlines 无文件返回空，
// 引擎 Do() 自动回退日线卖出判定），全程不依赖网络。

// lifecycleScript 全天触发买入的极简脚本（A价格+A过滤涨停 对恒价数据恒真），
// 配合 A持仓N天{1} 产生稳定交易序列。
const lifecycleScript = `package main

import (
	sb "github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/strategy-tail/core"
)

func Strategy() []core.Variant {
	return []core.Variant{
		{Name: "基线·全买", Buyer: sb.And{sb.A价格{Min: 2, Max: 120}, sb.A过滤涨停{}}},
	}
}
`

// TestServerScriptAPI 脚本读写与校验契约：
// GET 读到已写入内容；PUT 合法脚本落盘；PUT 非法脚本 400 且不覆盖原文件。
func TestServerScriptAPI(t *testing.T) {
	t.Chdir(t.TempDir())

	// 预置脚本文件
	if err := os.MkdirAll(filepath.Dir(ScriptPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ScriptPath, []byte(sampleScript), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewServer().Handler()

	// GET 读回
	res := doReq(t, h, http.MethodGet, "/api/script", nil, http.StatusOK)
	var got struct{ Content string }
	if err := json.Unmarshal(res, &got); err != nil {
		t.Fatal(err)
	}
	if got.Content != sampleScript {
		t.Fatal("GET /api/script 内容与预置不一致")
	}

	// PUT 非法脚本：400 且原文件不变
	broken := map[string]any{"content": "package main\nfunc broken( {}"}
	res = doReq(t, h, http.MethodPut, "/api/script", broken, http.StatusBadRequest)
	if !strings.Contains(string(res), "error") {
		t.Fatalf("错误响应缺少 error 字段: %s", res)
	}
	data, _ := os.ReadFile(ScriptPath)
	if string(data) != sampleScript {
		t.Fatal("非法脚本不应覆盖原文件")
	}

	// PUT 合法脚本：落盘
	doReq(t, h, http.MethodPut, "/api/script", map[string]any{"content": lifecycleScript}, http.StatusOK)
	data, _ = os.ReadFile(ScriptPath)
	if string(data) != lifecycleScript {
		t.Fatal("PUT 后文件内容未更新")
	}

	// POST check：合法 ok=true；非法 ok=false + error
	res = doReq(t, h, http.MethodPost, "/api/script/check", map[string]any{"content": lifecycleScript}, http.StatusOK)
	if !strings.Contains(string(res), `"ok":true`) {
		t.Fatalf("check 合法脚本应 ok: %s", res)
	}
	res = doReq(t, h, http.MethodPost, "/api/script/check", broken, http.StatusOK)
	if !strings.Contains(string(res), `"ok":false`) || !strings.Contains(string(res), "error") {
		t.Fatalf("check 非法脚本应报错: %s", res)
	}
}

// TestServerRunLifecycle 完整生命周期：
// run → status 轮询到 done → report/latest → 落盘产物 → reports 列表 → report/{id} → kline。
func TestServerRunLifecycle(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// 伪造数据源：sh600000 从 2024-06 起约 250 个交易日恒价上行（每日 +5%，不触涨停过滤）
	// 直接字面量构造 PullKline（DayKlines/MinKlines 仅依赖 Config.Dir，不建 update.db 连接）
	old := common.Pull
	common.Pull = &extend.PullKline{Config: extend.PullKlineConfig{Dir: dir}}
	t.Cleanup(func() { common.Pull = old })
	writeFakeDayDB(t, dir, "sh600000", 250, time.Date(2024, 6, 1, 0, 0, 0, 0, time.Local))

	// 写入脚本
	if err := os.MkdirAll(filepath.Dir(ScriptPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ScriptPath, []byte(lifecycleScript), 0644); err != nil {
		t.Fatal(err)
	}

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
	cfg := map[string]any{
		"startYear": 2025, "endYear": 2025,
		"sampleMode": "codes", "sampleCodes": []string{"sh600000"},
		"holdingDays": 1,
	}
	res = doReq(t, h, http.MethodPost, "/api/run", cfg, http.StatusOK)
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
	if rep.Config.ScriptName != "matrix" || len(rep.Variants) != 1 {
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
	csvs, _ := filepath.Glob(filepath.Join("output", "trades", "matrix", "*.csv"))
	if len(csvs) == 0 {
		t.Fatal("变体 CSV 未落盘")
	}
	htmls, _ := filepath.Glob(filepath.Join("output", "trades", "matrix", "*_summary.html"))
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

// writeFakeDayDB 写入伪造日K sqlite（与 updateDayKline 相同的表结构）。
// 每天开10收10.5（+5%，不触发涨停过滤），恒价保证买入条件每日为真。
func writeFakeDayDB(t *testing.T, dir, code string, days int, base time.Time) {
	t.Helper()
	db, err := xorms.NewSqlite(filepath.Join(dir, extend.DirDay, code+".db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Sync2(new(extend.Kline)); err != nil {
		t.Fatal(err)
	}
	rows := make([]*extend.Kline, 0, days)
	for i := 0; i < days; i++ {
		tm := base.AddDate(0, 0, i)
		rows = append(rows, &extend.Kline{
			Unix: tm.Unix(),
			Kline: &protocol.Kline{
				Time:   tm,
				Open:   protocol.Yuan(10),
				Close:  protocol.Yuan(10.5),
				High:   protocol.Yuan(10.6),
				Low:    protocol.Yuan(9.9),
				Volume: 10000,
			},
		})
	}
	if _, err := db.Insert(rows); err != nil {
		t.Fatal(err)
	}
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
