package lab

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/v2/protocol"
)

// replayFixture 构造待保存报告（变体 0 有回放、变体 1 无记录）与已加载行情副本。
func replayFixture(t *testing.T) (*Report, []*ReplayVariant, map[string]extend.Klines) {
	t.Helper()
	day := ReplayDay{Date: "2025-01-02", Equity: 10000, Cash: 8998, MarketValue: 1002, PositionRatio: 10.02,
		Holdings: []ReplayHolding{{PositionID: 1, Code: "sh600000", BuyTime: "2025-01-02 15:00:00", Quantity: 100,
			BuyPrice: 10, BuyExecPrice: 10.01, TotalCost: 1002, CostPrice: 10.02, MarkPrice: 10, MarkDate: "2025-01-02",
			MarketValue: 1000, FloatingPnL: -2, FloatingRate: -0.1996}},
		Trades: []ReplayTrade{{Seq: 1, PositionID: 1, Code: "sh600000", Time: "2025-01-02 15:00:00", Side: "buy",
			Price: 10, ExecPrice: 10.01, Quantity: 100, Fee: 1}}}
	rv := &ReplayVariant{Version: replayVersion, VariantIndex: 0, VariantName: "V0",
		Dates: []string{day.Date}, Days: []ReplayDay{day}}
	rep := &Report{Config: RunConfig{ScriptName: "MACD参数回测"}, Source: "macd",
		StartedAt: "2025-01-01T10:00:00+08:00", FinishedAt: "2025-01-01T10:00:01+08:00",
		Variants: []VariantReport{
			{Name: "V0", Replay: &ReplayMeta{Version: replayVersion, Available: true}},
			{Name: "V1", Replay: &ReplayMeta{Version: replayVersion, Available: false}},
		}}
	ks := extend.Klines{}
	for i := 0; i < 3; i++ {
		date := time.Date(2025, 1, 2+i, 0, 0, 0, 0, time.Local)
		ks = append(ks, &extend.Kline{Unix: date.Unix(), Kline: &protocol.Kline{Time: date,
			Open: 10, Close: 10 + float64(i), High: 11, Low: 9}})
	}
	return rep, []*ReplayVariant{rv, nil}, map[string]extend.Klines{"sh600000": ks}
}

func TestSaveReportReplayRoundTrip(t *testing.T) {
	t.Chdir(t.TempDir())
	rep, replays, loaded := replayFixture(t)
	if err := saveReport(rep, replays, loaded); err != nil {
		t.Fatal(err)
	}
	id := rep.RunID()
	// 回放明细往返一致。
	b, err := os.ReadFile(filepath.Join("output", "trades", id, "replay", "variant-0.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got ReplayVariant
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != replayVersion || got.VariantIndex != 0 || got.VariantName != "V0" || len(got.Days) != 1 {
		t.Fatalf("replay roundtrip mismatch: %+v", got)
	}
	if len(got.Days[0].Holdings) != 1 || got.Days[0].Holdings[0].Code != "sh600000" ||
		len(got.Days[0].Trades) != 1 || got.Days[0].Trades[0].ExecPrice != 10.01 {
		t.Fatalf("day roundtrip mismatch: %+v", got.Days[0])
	}
	// 未记录变体不产生文件。
	if _, err := os.Stat(filepath.Join("output", "trades", id, "replay", "variant-1.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unrecorded variant should have no file: %v", err)
	}
	// K 线快照只含买入股票，行为已加载区间。
	b, err = os.ReadFile(filepath.Join("output", "trades", id, "kline", "sh600000.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rows []klineRow
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].Date != "2025-01-02" || rows[0].Close != 10 || rows[2].Close != 12 {
		t.Fatalf("kline snapshot mismatch: %+v", rows)
	}
	entries, err := os.ReadDir(filepath.Join("output", "trades", id, "kline"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "sh600000.json" {
		t.Fatalf("kline dir should only contain bought stocks: %v", entries)
	}
	// report.json 中的回放能力描述随报告保存。
	b, err = os.ReadFile(filepath.Join("output", "trades", id, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved Report
	if err := json.Unmarshal(b, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Variants[0].Replay == nil || !saved.Variants[0].Replay.Available || saved.Variants[0].Replay.Version != replayVersion {
		t.Fatalf("replay meta lost: %+v", saved.Variants[0].Replay)
	}
	if saved.Variants[1].Replay == nil || saved.Variants[1].Replay.Available {
		t.Fatalf("unrecorded variant meta mismatch: %+v", saved.Variants[1].Replay)
	}
}

func TestReplayUnknownVersionRejected(t *testing.T) {
	t.Chdir(t.TempDir())
	rep, replays, loaded := replayFixture(t)
	if err := saveReport(rep, replays, loaded); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("output", "trades", rep.RunID(), "replay", "variant-0.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rv ReplayVariant
	if err := json.Unmarshal(b, &rv); err != nil {
		t.Fatal(err)
	}
	rv.Version = 99
	b, err = json.MarshalIndent(rv, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0644); err != nil {
		t.Fatal(err)
	}
	_, err = readReplay(rep.RunID(), 0)
	var ve *replayVersionError
	if !errors.As(err, &ve) {
		t.Fatalf("unknown version should be rejected: %v", err)
	}
	if msg := ve.Error(); !strings.Contains(msg, "重新回测") {
		t.Fatalf("message should prompt re-backtest: %s", msg)
	}
}

func TestSaveReportFailureKeepsPublishedReport(t *testing.T) {
	t.Chdir(t.TempDir())
	rep, replays, loaded := replayFixture(t)
	if err := saveReport(rep, replays, loaded); err != nil {
		t.Fatal(err)
	}
	id := rep.RunID()
	published, err := os.ReadFile(filepath.Join("output", "trades", id, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	// 注入失败：把回放明细替换为同名目录，发布阶段 rename 失败。
	path := filepath.Join("output", "trades", id, "replay", "variant-0.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	if err := saveReport(rep, replays, loaded); err == nil {
		t.Fatal("expected save failure on poisoned replay file")
	}
	again, err := os.ReadFile(filepath.Join("output", "trades", id, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(published) {
		t.Fatal("failed save must not republish report.json (R12)")
	}
}

func TestReplayAPI(t *testing.T) {
	t.Chdir(t.TempDir())
	rep, replays, loaded := replayFixture(t)
	if err := saveReport(rep, replays, loaded); err != nil {
		t.Fatal(err)
	}
	h := NewServer().Handler()
	id := rep.RunID()

	// 正常变体返回完整明细。
	var rv ReplayVariant
	if err := json.Unmarshal(doReq(t, h, http.MethodGet, "/api/report/"+id+"/replay/0", nil, http.StatusOK), &rv); err != nil {
		t.Fatal(err)
	}
	if rv.VariantName != "V0" || len(rv.Days) != 1 {
		t.Fatalf("replay API mismatch: %+v", rv)
	}
	// 非法序号 / 越界 / 非法报告 ID。
	doReq(t, h, http.MethodGet, "/api/report/"+id+"/replay/abc", nil, http.StatusBadRequest)
	doReq(t, h, http.MethodGet, "/api/report/"+id+"/replay/-1", nil, http.StatusBadRequest)
	doReq(t, h, http.MethodGet, "/api/report/"+id+"/replay/9", nil, http.StatusBadRequest)
	doReq(t, h, http.MethodGet, "/api/report/a%2Fb/replay/0", nil, http.StatusBadRequest)

	// 旧报告（无 replay 描述、无回放文件）提示重新回测。
	legacy := &Report{Config: RunConfig{ScriptName: "MACD参数回测"}, Source: "macd",
		StartedAt: "2025-01-02T10:00:00+08:00", FinishedAt: "2025-01-02T10:00:01+08:00",
		Variants: []VariantReport{{Name: "Old"}}}
	legacyDir := filepath.Join("output", "trades", legacy.RunID())
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "report.json"), b, 0644); err != nil {
		t.Fatal(err)
	}
	doReq(t, h, http.MethodGet, "/api/report/"+legacy.RunID()+"/replay/0", nil, http.StatusBadRequest)

	// K 线快照：读取、未保存股票 404、非法代码 400。
	var rows []klineRow
	if err := json.Unmarshal(doReq(t, h, http.MethodGet, "/api/report/"+id+"/replay/kline/sh600000", nil, http.StatusOK), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("kline rows = %d", len(rows))
	}
	doReq(t, h, http.MethodGet, "/api/report/"+id+"/replay/kline/sz000001", nil, http.StatusNotFound)
	// 编码斜杠 %2F 解转义后成两段，不匹配 {code} 单段通配，在路由层即 404（安全拒绝）。
	doReq(t, h, http.MethodGet, "/api/report/"+id+"/replay/kline/a%2Fx", nil, http.StatusNotFound)
	// 单段非法代码（含空格）进入 handler，由 validReplayCode 拒绝 400。
	doReq(t, h, http.MethodGet, "/api/report/"+id+"/replay/kline/sh%20600", nil, http.StatusBadRequest)
	// 函数层注入防护（路由层对 .. 会先做路径规范化，不经过 handler）。
	if validReplayCode("../x") || validReplayCode("a/b") || validReplayCode("") || validReplayCode("sh 6000") {
		t.Fatal("invalid codes must be rejected")
	}
	if !validReplayCode("sh600000") {
		t.Fatal("valid code rejected")
	}
	if _, err := readReplayKline(id, "../etc"); err == nil {
		t.Fatal("path traversal must be rejected")
	}

	// 删除报告时回放目录随之删除。
	doReq(t, h, http.MethodDelete, "/api/report/"+id, nil, http.StatusOK)
	if _, err := os.Stat(filepath.Join("output", "trades", id, "replay")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replay dir should be removed with report: %v", err)
	}
}
