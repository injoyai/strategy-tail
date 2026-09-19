package lab

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// topNScript TopN 买入脚本：动量(2) 降序第 1 名，每日只买横截面最强一票。
const topNScript = `package main

import (
	"github.com/injoyai/strategy-tail/core"
	f "github.com/injoyai/strategy-tail/strategies/factor"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
)

func Strategy() []core.Variant {
	return []core.Variant{
		{Name: "动量TopN1", Buyer: sb.A因子TopN{Factor: f.N日动量{Days: 2}, N: 1, Asc: false}},
	}
}
`

// TestServerTopNRun 端到端：回测前填充快照 → 仅买 TopN 票 → 任务结束清理。
func TestServerTopNRun(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	old := common.Pull
	common.Pull = &extend.PullKline{Config: extend.PullKlineConfig{Dir: dir}}
	t.Cleanup(func() { common.Pull = old })

	// 2024 年 8 根垫底（his 非空）+ 2025 年 8 根：
	// 001 动量(2) 每日严格高于 002（001 有涨有跌、002 恒跌）→ 001 每天都是
	// 降序第 1 名（无并列）。001 必须产生亏损交易：全胜时 core.Stats 的
	// 盈亏比为 +Inf（有意设计），json 序列化失败导致报告落盘报错。
	base := time.Date(2024, 12, 24, 0, 0, 0, 0, time.Local)
	writeDayDBCloses(t, dir, "sh600001", []float64{
		9.5, 9.5, 9.5, 9.5, 9.5, 9.5, 9.5, 9.5,
		10, 10.2, 10.4, 10.6, 10.55, 10.5, 10.8, 11,
	}, base)
	writeDayDBCloses(t, dir, "sh600002", []float64{
		21, 21, 21, 21, 21, 21, 21, 21,
		20, 19.8, 19.6, 19.4, 19.2, 19, 18.8, 18.6,
	}, base)

	if err := os.MkdirAll(filepath.Dir(ScriptPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ScriptPath, []byte(topNScript), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewServer().Handler()

	cfg := map[string]any{
		"startYear": 2025, "endYear": 2025,
		"sampleMode": "codes", "sampleCodes": []string{"sh600001", "sh600002"},
		"holdingDays": 1,
	}
	res := doReq(t, h, http.MethodPost, "/api/run", cfg, http.StatusOK)
	if !bytes.Contains(res, []byte(`"ok":true`)) {
		t.Fatalf("run 响应异常: %s", res)
	}

	// 轮询至完成（模板同 TestServerRunLifecycle）
	deadline := time.Now().Add(30 * time.Second)
	var st map[string]any
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
	if n := st["totalCodes"].(float64); n != 2 {
		t.Fatalf("totalCodes=%v", n)
	}

	// 交易只应来自横截面第 1 名 sh600001
	res = doReq(t, h, http.MethodGet, "/api/report/latest", nil, http.StatusOK)
	var rep Report
	if err := json.Unmarshal(res, &rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.Variants) != 1 {
		t.Fatalf("变体数 = %d", len(rep.Variants))
	}
	if len(rep.Variants[0].Trades) == 0 {
		t.Fatal("应产生交易")
	}
	for _, tr := range rep.Variants[0].Trades {
		if tr.Code != "sh600001" {
			t.Fatalf("非 TopN 票被买入: %+v", tr)
		}
	}

	// 任务结束快照已清理
	day := core.DayOf(base.AddDate(0, 0, 15))
	key := core.TopNKey("N日动量(2)", false)
	if got := core.CrossSectionRank(day, key, "sh600001"); got != 0 {
		t.Fatalf("快照未清理: 名次 = %d", got)
	}
}

// TestServerStrategyRunLifecycle 简单模式端到端：/api/strategy/run 合法请求 →
// 4 个稳定顺序变体 → 最新报告含 source/strategySpec/comparison；
// 全程不读取、不写入 ScriptPath（脚本文件不存在也能运行）。
func TestServerStrategyRunLifecycle(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	old := common.Pull
	common.Pull = &extend.PullKline{Config: extend.PullKlineConfig{Dir: dir}}
	t.Cleanup(func() { common.Pull = old })
	writePresetDayDB(t, dir, "sh600000", 600, time.Date(2024, 6, 1, 0, 0, 0, 0, time.Local))

	h := NewServer().Handler()

	res := doReq(t, h, http.MethodPost, "/api/strategy/run", simpleRunBody(), http.StatusOK)
	for _, want := range []string{`"ok":true`, `"variants":4`, `"source":"simple"`} {
		if !bytes.Contains(res, []byte(want)) {
			t.Fatalf("run 响应缺少 %s: %s", want, res)
		}
	}

	// 轮询至完成（模板同 TestServerRunLifecycle）
	deadline := time.Now().Add(30 * time.Second)
	var st map[string]any
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
			t.Fatalf("简单模式回测超时未完成: %v", st)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st["state"] != "done" {
		t.Fatalf("简单模式回测失败: %v", st)
	}

	// 最新报告：source/strategySpec/comparison 与 N+2 顺序
	res = doReq(t, h, http.MethodGet, "/api/report/latest", nil, http.StatusOK)
	var rep Report
	if err := json.Unmarshal(res, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Source != "simple" || rep.StrategySpec == nil || rep.Comparison == nil {
		t.Fatalf("报告缺少简单模式元数据: source=%q spec=%v cmp=%v",
			rep.Source, rep.StrategySpec, rep.Comparison)
	}
	if len(rep.Variants) != 4 {
		t.Fatalf("变体数 = %d, want 4", len(rep.Variants))
	}
	prefixes := []string{"基准 · ", "单条件 1 · ", "单条件 2 · ", "组合增强 · "}
	for i, pre := range prefixes {
		if !strings.HasPrefix(rep.Variants[i].Name, pre) {
			t.Fatalf("变体[%d] = %q, want 前缀 %q", i, rep.Variants[i].Name, pre)
		}
	}
	if rep.Comparison.BaselineVariant != rep.Variants[0].Name ||
		rep.Comparison.CombinedVariant != rep.Variants[3].Name {
		t.Fatalf("Comparison 基准/组合指向异常: %+v", rep.Comparison)
	}
	if len(rep.Comparison.FactorVariants) != 2 {
		t.Fatalf("单条件摘要数 = %d, want 2", len(rep.Comparison.FactorVariants))
	}

	// 简单模式不读写脚本：ScriptPath 自始至终不存在
	if _, err := os.Stat(ScriptPath); !os.IsNotExist(err) {
		t.Fatalf("简单模式不应读写脚本文件: %v", err)
	}
}

// TestServerFactorsAPI 端到端：/api/factors 直出目录，新字段与既有字段并存。
func TestServerFactorsAPI(t *testing.T) {
	h := NewServer().Handler()
	res := doReq(t, h, http.MethodGet, "/api/factors", nil, http.StatusOK)
	var catalog []map[string]any
	if err := json.Unmarshal(res, &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 20 {
		t.Fatalf("因子目录项数 = %d", len(catalog))
	}
	for _, e := range catalog {
		for _, k := range []string{"kind", "name", "description",
			"category", "parameterLabel", "defaultDays", "unit", "example"} {
			if _, ok := e[k]; !ok {
				t.Fatalf("目录项缺字段 %s: %v", k, e)
			}
		}
		if v, ok := e["defaultDays"].(float64); !ok || v <= 0 {
			t.Fatalf("defaultDays 非法: %v", e["defaultDays"])
		}
		if v, ok := e["implementationVersion"].(float64); !ok || v <= 0 {
			t.Fatalf("implementationVersion 非法: %v", e["implementationVersion"])
		}
	}
}

// TestServerAnalysisAPI 端到端：因子目录 → 非法 kind 409 → 合法分析 → task=analysis → 最新报告。
func TestServerAnalysisAPI(t *testing.T) {
	dir := t.TempDir()
	setupAnalysisData(t, dir)

	base := time.Date(2024, 12, 24, 0, 0, 0, 0, time.Local)
	up := append([]float64{9.5, 9.5, 9.5, 9.5, 9.5, 9.5, 9.5, 9.5},
		10, 10.2, 10.4, 10.6, 10.8, 11, 11.2, 11.4, 11.6, 11.8,
		12, 12.2, 12.4, 12.6, 12.8, 13, 13.2, 13.4, 13.6, 13.8)
	down := append([]float64{21, 21, 21, 21, 21, 21, 21, 21},
		20.8, 20.6, 20.4, 20.2, 20, 19.8, 19.6, 19.4, 19.2, 19,
		18.8, 18.6, 18.4, 18.2, 18, 17.8, 17.6, 17.4, 17.2, 17)
	writeDayDBCloses(t, dir, "sh600001", up, base)
	writeDayDBCloses(t, dir, "sh600002", down, base)

	h := NewServer().Handler()

	// 因子目录：20 项，每项含 kind/name/description
	res := doReq(t, h, http.MethodGet, "/api/factors", nil, http.StatusOK)
	var catalog []map[string]any
	if err := json.Unmarshal(res, &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 20 {
		t.Fatalf("因子目录项数 = %d", len(catalog))
	}
	for _, e := range catalog {
		for _, k := range []string{"kind", "name", "description"} {
			if _, ok := e[k]; !ok {
				t.Fatalf("目录项缺字段 %s: %v", k, e)
			}
		}
	}

	// 未知因子 → StartAnalysis 校验失败 → 409
	res = doReq(t, h, http.MethodPost, "/api/analyze", map[string]any{
		"kind": "nope",
	}, http.StatusConflict)

	// 合法分析请求
	res = doReq(t, h, http.MethodPost, "/api/analyze", map[string]any{
		"startYear": 2025, "endYear": 2025,
		"sampleMode": "codes", "sampleCodes": []string{"sh600001", "sh600002"},
		"kind": "momentum", "days": 2, "window": 1,
	}, http.StatusOK)
	if !bytes.Contains(res, []byte(`"ok":true`)) {
		t.Fatalf("analyze 响应异常: %s", res)
	}

	// 轮询至完成，task 应为 analysis
	deadline := time.Now().Add(30 * time.Second)
	var st map[string]any
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
			t.Fatalf("分析超时未完成: %v", st)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st["state"] != "done" {
		t.Fatalf("分析失败: %v", st)
	}
	if st["task"] != "analysis" {
		t.Fatalf("task = %v, want analysis", st["task"])
	}

	// 最新分析报告
	res = doReq(t, h, http.MethodGet, "/api/analysis/latest", nil, http.StatusOK)
	var rep AnalysisReport
	if err := json.Unmarshal(res, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.FactorName != "N日动量(2)" {
		t.Fatalf("FactorName = %q", rep.FactorName)
	}
	if !nearlyEq(rep.Stats.Mean, 1) {
		t.Fatalf("Mean = %v, want 1", rep.Stats.Mean)
	}
}
