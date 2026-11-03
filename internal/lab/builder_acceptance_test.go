package lab

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/tdx/v2/protocol"
)

func TestBuilderWriteBoundaries(t *testing.T) {
	t.Chdir(t.TempDir())
	h := NewServer().Handler()
	for _, path := range []string{"/api/builder/strategies", "/api/builder/strategies/st_20990101T000000000Z_00000000", "/api/builder/strategies/st_20990101T000000000Z_00000000/copy", "/api/builder/run"} {
		method := http.MethodPost
		if strings.HasSuffix(path, "00000000") {
			method = http.MethodPut
		}
		req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
		req.Header.Set("Origin", "https://example.test")
		req.Header.Set("Content-Type", "text/plain")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s: cross-site status %d", path, rec.Code)
		}
	}
	doReq(t, h, http.MethodPost, "/api/builder/run", map[string]any{"startYear": 2023, "endYear": 2023, "sampleMode": "all", "holdingDays": 1}, http.StatusBadRequest)
	req := runReq(baseSpec())
	req["sampleCodes"] = []string{"../../private"}
	doReq(t, h, http.MethodPost, "/api/builder/run", req, http.StatusBadRequest)
	req = runReq(baseSpec())
	req["startYear"] = 1
	doReq(t, h, http.MethodPost, "/api/builder/run", req, http.StatusBadRequest)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/builder/strategies", strings.NewReader(`{"spec":`+strings.Repeat(" ", maxCandidateBody)+`{}}`)))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize status=%d", rec.Code)
	}
}

func TestBuilderRiskAndListValidation(t *testing.T) {
	for _, mode := range []string{"save", "run"} {
		spec := baseSpec()
		spec.Risks = []*BuilderNode{bGroup("and", bCond("risk_fixed_stop", nil), bCond("risk_fixed_tp", nil))}
		if err := spec.Validate(mode); err == nil {
			t.Fatalf("%s accepted risk group", mode)
		}
	}
	for _, periods := range [][]any{{0.0, 20.0}, {-1.0, 20.0}, {5.0, 1001.0}, {5.0}, {5.5, 20.0}} {
		spec := baseSpec()
		spec.Buy = bCond("buy_ma_bull", map[string]any{"periods": periods})
		if err := spec.Validate("run"); err == nil {
			t.Fatalf("accepted periods %v", periods)
		}
	}
}

func builderBars(n int) extend.Klines {
	bars := make(extend.Klines, 0, n)
	for i := 0; i < n; i++ {
		at := time.Date(2025, 1, 2+i, 0, 0, 0, 0, time.Local)
		close := 20 + 0.1*float64(i) + 0.5*math.Sin(float64(i)*2*math.Pi/30)
		bars = append(bars, &extend.Kline{Unix: at.Unix(), FloatStock: 10000000000, Kline: &protocol.Kline{Time: at, Open: close, Close: close, High: close + 0.2, Low: close - 0.2, Volume: 10000}})
	}
	return bars
}

func TestBuilderLargeWindowInsufficientHistory(t *testing.T) {
	spec := BuilderSpec{
		Version: 1, Name: "大窗口边界", HoldToEnd: true,
		Buy: bCond("buy_bottom_up", map[string]any{"window": 500.0}),
	}
	if err := spec.Validate("run"); err != nil {
		t.Fatal(err)
	}
	buyer, err := spec.Buyer()
	if err != nil {
		t.Fatal(err)
	}
	if buyer.Buy("sh600001", builderBars(60)) {
		t.Fatal("insufficient history must not trigger a buy")
	}
}

func TestBuilderTemplateTradesMatchCLI(t *testing.T) {
	spec := BuilderTemplates()[0].Spec
	buyer, err := spec.Buyer()
	if err != nil {
		t.Fatal(err)
	}
	seller, err := spec.Seller()
	if err != nil {
		t.Fatal(err)
	}
	cfg := defaultMACDConfig()
	bars := builderBars(320)
	wantBT := core.Backtest{Buyer: cfg.MACD.buyer(), Seller: cfg.seller(), Cost: core.DefaultCost(), Position: core.PositionConfig{MaxPerCode: 1}}
	want := wantBT.Do("sh600000", cloneBuilderBars(bars[:60]), cloneBuilderBars(bars[60:]), nil)
	gotBT := wantBT
	gotBT.Buyer = buyer
	gotBT.Seller = seller
	got := gotBT.Do("sh600000", cloneBuilderBars(bars[:60]), cloneBuilderBars(bars[60:]), nil)
	if len(want) == 0 {
		t.Fatal("fixture did not exercise trades")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("builder trades differ from direct CLI composition")
	}
}

func TestBuilderGroupingAndIndependentStop(t *testing.T) {
	spec := baseSpec()
	spec.Buy = bGroup("and", bCond("buy_all", nil), bGroup("or", bCond("buy_price", map[string]any{"max": 9.0}), bCond("buy_price", map[string]any{"min": 11.0})))
	buyer, err := spec.Buyer()
	if err != nil {
		t.Fatal(err)
	}
	bars := builderBars(3)
	bars[0].Close = 12
	if !buyer.Buy("sh600000", bars[:1]) {
		t.Fatal("nested OR did not match")
	}
	flat := sb.And{sb.A价格{Max: 9}, sb.A价格{Min: 11}}
	if flat.Buy("sh600000", bars[:1]) {
		t.Fatal("fixture must distinguish OR from flat AND")
	}
	bars[0].Close = 10
	if buyer.Buy("sh600000", bars[:1]) {
		t.Fatal("neither OR branch should match")
	}
	spec = baseSpec()
	spec.Buy = bCond("buy_all", nil)
	spec.Risks = []*BuilderNode{bCond("risk_fixed_stop", map[string]any{"pct": 5.0})}
	spec.SellSignal = bGroup("or", bCond("sell_macd_reversal", map[string]any{"lookback": 10.0, "minDiff": 1.0}))
	seller, err := spec.Seller()
	if err != nil {
		t.Fatal(err)
	}
	bars[1].Close = 9
	bars[2].Close = 8
	signal, _ := spec.SellSignal.Seller()
	if signal.Sell("sh600000", bars[:2], core.Buy{Price: 10}) {
		t.Fatal("fixture must leave MACD signal false")
	}
	bt := core.Backtest{Buyer: sb.A全部{}, Seller: seller, Position: core.PositionConfig{MaxPerCode: 1}}
	trades := bt.Do("sh600000", nil, cloneBuilderBars(bars), nil)
	if len(trades) == 0 || trades[0].Virtual || !trades[0].BuyTime.Equal(bars[0].Time) || !trades[0].SellTime.Equal(bars[1].Time) {
		t.Fatalf("stop-loss/T+1 failed: %+v", trades)
	}
}

func TestBuilderDraftRunAndSnapshotIsolation(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	old := common.Pull
	common.Pull = &extend.PullKline{Config: extend.PullKlineConfig{Dir: dir}}
	t.Cleanup(func() { common.Pull = old })
	writeFakeDayDB(t, dir, "sh600000", 12, time.Date(2025, 1, 2, 0, 0, 0, 0, time.Local))
	server := NewServer()
	h := server.Handler()
	savedSpec := baseSpec()
	savedSpec.Buy = bCond("buy_price", map[string]any{"max": 12.0})
	savedSpec.SellSignal = nil
	savedSpec.HoldToEnd = true
	saved, err := server.builderStore.Create(savedSpec)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := server.builderStore.Get(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	draft.Spec.Buy.Params["max"] = 11.0
	req := runReq(draft.Spec)
	req["startYear"] = 2025
	req["endYear"] = 2025
	// 共享任务锁冲突不得改写草稿或策略库。
	server.runner.mu.Lock()
	doReq(t, h, http.MethodPost, "/api/builder/run", req, http.StatusConflict)
	server.runner.mu.Unlock()
	doReq(t, h, http.MethodPost, "/api/builder/run", req, http.StatusOK)
	deadline := time.Now().Add(5 * time.Second)
	for server.runner.running.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if server.runner.Status()["state"] != "done" {
		t.Fatalf("run failed: %v", server.runner.Status())
	}
	var report Report
	if err := json.Unmarshal(doReq(t, h, http.MethodGet, "/api/report/latest", nil, http.StatusOK), &report); err != nil {
		t.Fatal(err)
	}
	if report.Source != sourceBuilder || !reflect.DeepEqual(report.Config.Builder, &draft.Spec) || report.Coverage.Completed != 1 || len(report.Variants[0].Trades) == 0 {
		t.Fatalf("snapshot/coverage missing: %+v", report)
	}
	for _, trade := range report.Variants[0].Trades {
		if !trade.Virtual {
			t.Fatal("hold-to-end must mark Virtual")
		}
	}
	original, _ := server.builderStore.Get(saved.ID)
	if original.Spec.Buy.Params["max"] != 12.0 {
		t.Fatal("running draft updated library")
	}
	if _, err := server.builderStore.Update(saved.ID, draft.Spec, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := server.builderStore.Copy(saved.ID, "副本"); err != nil {
		t.Fatal(err)
	}
	var persisted Report
	if err := json.Unmarshal(doReq(t, h, http.MethodGet, "/api/report/"+report.RunID(), nil, http.StatusOK), &persisted); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(persisted.Config.Builder, report.Config.Builder) {
		t.Fatal("saved edits changed historical snapshot")
	}
}

func cloneBuilderBars(bars extend.Klines) extend.Klines {
	out := make(extend.Klines, len(bars))
	for i, bar := range bars {
		value, kline := *bar, *bar.Kline
		value.Kline = &kline
		out[i] = &value
	}
	return out
}
