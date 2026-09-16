package lab

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
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
