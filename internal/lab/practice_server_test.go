package lab

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// practice_server_test.go 盘感练习 HTTP 接口测试（实施文档 §4）：
// 完整练习流程（出题→买入→推进→卖出→结束→复盘→重练）、活跃/列表响应的
// 信息隔离（不泄露股票代码、真实日期与数据范围）、请求错误映射（400/404/409）、
// 以及策略或行情更新后旧题与重练的快照隔离。
// 全部使用临时目录与注入的固定行情，不触碰真实行情库与策略库。

// practiceAPISrc 八根固定日 K（2024-01-02 起 UTC，整元/半元收盘价）。
func practiceAPISrc() practiceKlineSource {
	closes := []float64{10, 10.5, 11, 10.8, 11.2, 11.5, 11, 11.3}
	return fakePracticeSource{
		codes: []string{"sh600000"},
		klines: map[string]extend.Klines{
			"sh600000": practiceTestKlinesFrom(time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), closes),
		},
	}
}

// newPracticeServerForTest 构造带临时存储、固定行情与固定抽样（取第一命中）
// 的 Server，返回 Server 与 Handler。
func newPracticeServerForTest(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	dir := t.TempDir()
	s := newServerWithStores(
		NewAnalysisStore(filepath.Join(dir, "ana")),
		NewCandidateStore(filepath.Join(dir, "cand")))
	s.builderStore = NewBuilderStore(filepath.Join(dir, "builder"))
	s.practiceStore = NewPracticeStore(filepath.Join(dir, "practice"))
	s.wirePracticeStores()
	s.runner.practiceSource = practiceAPISrc()
	s.runner.practiceChoose = func(n int) bool { return n == 1 } // 取第一命中
	return s, s.Handler()
}

// practiceTryReq 发送请求并返回状态码与响应体（用于需容忍任务收尾竞态的断言）。
func practiceTryReq(t *testing.T, h http.Handler, method, path string, body any) (int, []byte) {
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
	return rec.Code, rec.Body.Bytes()
}

// practiceStartAndAwait 经 API 出题并轮询到 active，返回会话 ID。
func practiceStartAndAwait(t *testing.T, h http.Handler, strategyID, requestID string) string {
	t.Helper()
	res := doReq(t, h, http.MethodPost, "/api/practice", map[string]any{
		"strategyId": strategyID, "strategyVersion": 1,
		"fromYear": 2024, "toYear": 2024, "requestId": requestID,
	}, http.StatusAccepted)
	var started struct {
		SessionID string `json:"sessionId"`
		Started   bool   `json:"started"`
	}
	if err := json.Unmarshal(res, &started); err != nil {
		t.Fatal(err)
	}
	if started.SessionID == "" || !started.Started {
		t.Fatalf("出题受理响应不正确: %s", res)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		st := doReq(t, h, http.MethodGet, "/api/practice/"+started.SessionID, nil, http.StatusOK)
		var view practiceSessionView
		if err := json.Unmarshal(st, &view); err != nil {
			t.Fatal(err)
		}
		if view.Phase == practicePhaseActive {
			return started.SessionID
		}
		if view.Phase != practicePhasePreparing {
			t.Fatalf("出题未进入 active: %s", st)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("出题 5s 内未完成")
	return ""
}

// practiceDoAction 提交练习动作并解析返回的会话视图。
func practiceDoAction(t *testing.T, h http.Handler, id, action, commandID string, expectedRevision int64) practiceSessionView {
	t.Helper()
	res := doReq(t, h, http.MethodPost, "/api/practice/"+id+"/actions", map[string]any{
		"action": action, "commandId": commandID, "expectedRevision": expectedRevision,
	}, http.StatusOK)
	var view practiceSessionView
	if err := json.Unmarshal(res, &view); err != nil {
		t.Fatal(err)
	}
	return view
}

// practiceLeakedFields 检查盲练响应是否泄露股票身份、真实日期或数据范围；
// 返回首个命中的字段名（空串表示未泄露）。
func practiceLeakedFields(raw []byte) string {
	for _, key := range []string{`"code"`, `"date"`, `"unix"`, `"coverage"`} {
		if bytes.Contains(raw, []byte(key)) {
			return key
		}
	}
	return ""
}

// practiceAwaitStartIdempotent 同 requestID 重放出题请求：容忍任务收尾竞态
// 的 409（busy），最终应幂等返回同一会话且 started=false。
func practiceAwaitStartIdempotent(t *testing.T, h http.Handler, body map[string]any, wantID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		code, res := practiceTryReq(t, h, http.MethodPost, "/api/practice", body)
		if code == http.StatusConflict {
			time.Sleep(5 * time.Millisecond) // 前一任务尚未释放互斥，稍后重试
			continue
		}
		if code != http.StatusAccepted {
			t.Fatalf("重放出题应 202: %d %s", code, res)
		}
		var started struct {
			SessionID string `json:"sessionId"`
			Started   bool   `json:"started"`
		}
		if err := json.Unmarshal(res, &started); err != nil {
			t.Fatal(err)
		}
		if started.SessionID != wantID || started.Started {
			t.Fatalf("重放应幂等返回同一会话且不开新任务: %s", res)
		}
		return
	}
	t.Fatalf("重放出题 5s 内未成功")
}

// TestPracticeAPIFlow 完整练习流程：出题→买入（幂等重放、重复买入拒绝）→
// 推进→卖出→结束→复盘披露身份与真实日期→列表不泄露→重练另建记录。
func TestPracticeAPIFlow(t *testing.T) {
	s, h := newPracticeServerForTest(t)
	st, err := s.builderStore.Create(practiceTestSpec())
	if err != nil {
		t.Fatalf("创建策略: %v", err)
	}
	startBody := map[string]any{
		"strategyId": st.ID, "strategyVersion": 1,
		"fromYear": 2024, "toYear": 2024, "requestId": "flow-req-1",
	}
	id := practiceStartAndAwait(t, h, st.ID, "flow-req-1")
	practiceAwaitStartIdempotent(t, h, startBody, id)

	// 初始视图：仅信号日已揭示，无任何身份与日期信息。
	res := doReq(t, h, http.MethodGet, "/api/practice/"+id, nil, http.StatusOK)
	if leak := practiceLeakedFields(res); leak != "" {
		t.Fatalf("活跃视图泄露敏感字段 %s: %s", leak, res)
	}
	var v practiceSessionView
	if err := json.Unmarshal(res, &v); err != nil {
		t.Fatal(err)
	}
	if v.Phase != practicePhaseActive || v.Stage != practiceStageWaiting ||
		v.Revision != 1 || v.CurrentDay != 0 || v.Quantity != 0 || v.BuyDay != nil {
		t.Fatalf("初始视图不正确: %+v", v)
	}
	if v.BudgetYuan != practiceBudgetYuan || v.StrategyName != practiceTestSpec().Name {
		t.Fatalf("开题快照字段不正确: budget=%v name=%q", v.BudgetYuan, v.StrategyName)
	}
	if len(v.Days) != 1 || v.Days[0].Day != 0 || v.Days[0].Close != 10 || v.CurrentPrice != 10 {
		t.Fatalf("已揭示行情不正确: %+v", v.Days)
	}
	if !v.CanBuy || !v.CanSkip || !v.CanNext || !v.CanFinish || v.CanSell {
		t.Fatalf("可用操作不正确: %+v", v)
	}

	// 买入：按当前收盘价成交，100 股取整、含费不超预算（费用快照来自环境配置）。
	v = practiceDoAction(t, h, id, "buy", "f-c1", 1)
	if v.Revision != 2 || v.Stage != practiceStageHolding || v.Quantity <= 0 || v.Quantity%100 != 0 {
		t.Fatalf("买入后状态不正确: %+v", v)
	}
	if v.BuyCostYuan <= 0 || v.BuyCostYuan > practiceBudgetYuan {
		t.Fatalf("买入成本不正确: %v", v.BuyCostYuan)
	}
	if v.BuyDay == nil || *v.BuyDay != 0 {
		t.Fatalf("买入日不正确: %+v", v.BuyDay)
	}

	// 同 commandId 幂等重放：状态不变。
	v = practiceDoAction(t, h, id, "buy", "f-c1", 1)
	if v.Revision != 2 || v.Stage != practiceStageHolding {
		t.Fatalf("重放应幂等: rev=%d stage=%s", v.Revision, v.Stage)
	}

	// 持仓期间再买 → 400（每题只买入一次）。
	doReq(t, h, http.MethodPost, "/api/practice/"+id+"/actions",
		map[string]any{"action": "buy", "commandId": "f-c2", "expectedRevision": 2}, http.StatusBadRequest)

	// 推进一日：可见第 2 根，T+1 后可卖。
	v = practiceDoAction(t, h, id, "next", "f-c3", 2)
	if v.Revision != 3 || v.CurrentDay != 1 || len(v.Days) != 2 || v.CurrentPrice != 10.5 {
		t.Fatalf("推进后视图不正确: %+v", v)
	}
	if !v.CanSell {
		t.Fatalf("次日应可卖出: %+v", v)
	}

	// 卖出：全部卖出并记录已实现收益。
	v = practiceDoAction(t, h, id, "sell", "f-c4", 3)
	if v.Revision != 4 || v.Stage != practiceStageSold || v.SellDay == nil || *v.SellDay != 1 {
		t.Fatalf("卖出后状态不正确: %+v", v)
	}
	if v.SellIncomeYuan <= 0 || v.Eval.RealizedPct <= 0 {
		t.Fatalf("卖出收益不正确: income=%v eval=%+v", v.SellIncomeYuan, v.Eval)
	}

	// 结束：finished + finished_sold。
	v = practiceDoAction(t, h, id, "finish", "f-c5", 4)
	if v.Phase != practicePhaseFinished || v.EndReason != practiceEndFinishedSold {
		t.Fatalf("结束状态不正确: %+v", v)
	}

	// 结束后普通视图仍盲（复盘接口才披露身份与日期）。
	res = doReq(t, h, http.MethodGet, "/api/practice/"+id, nil, http.StatusOK)
	if leak := practiceLeakedFields(res); leak != "" {
		t.Fatalf("结束视图泄露敏感字段 %s: %s", leak, res)
	}

	// 复盘：披露代码、真实日期、完整窗口与成交明细。
	res = doReq(t, h, http.MethodGet, "/api/practice/"+id+"/review", nil, http.StatusOK)
	var rv practiceReviewView
	if err := json.Unmarshal(res, &rv); err != nil {
		t.Fatal(err)
	}
	if rv.Code != "sh600000" || rv.StrategyName != practiceTestSpec().Name ||
		rv.EndReason != practiceEndFinishedSold || rv.SignalIndex != 0 {
		t.Fatalf("复盘身份不正确: %+v", rv)
	}
	if len(rv.Days) != 8 || rv.ReviewEndDay != 7 || rv.Days[0].Date != "2024-01-02" ||
		rv.Days[7].Date != "2024-01-09" || rv.DataEndDate != "2024-01-09" {
		t.Fatalf("复盘窗口不正确: end=%d days=%+v dataEnd=%q", rv.ReviewEndDay, rv.Days, rv.DataEndDate)
	}
	if len(rv.Trades) != 2 || rv.Trades[0].Action != "buy" || rv.Trades[0].Price != 10 ||
		rv.Trades[1].Action != "sell" || rv.Trades[1].Price != 10.5 || rv.Trades[1].Day != 1 {
		t.Fatalf("复盘成交明细不正确: %+v", rv.Trades)
	}
	if rv.Coverage == nil || rv.Coverage.Codes != 1 || rv.Coverage.Hits != 8 {
		t.Fatalf("复盘覆盖统计不正确: %+v", rv.Coverage)
	}
	if !bytes.Contains(res, []byte(`"code":"sh600000"`)) {
		t.Fatalf("复盘应含股票代码: %s", res)
	}

	// 列表：不泄露身份与日期。
	res = doReq(t, h, http.MethodGet, "/api/practices", nil, http.StatusOK)
	if leak := practiceLeakedFields(res); leak != "" {
		t.Fatalf("练习列表泄露敏感字段 %s: %s", leak, res)
	}
	var list struct {
		Practices []PracticeSummary `json:"practices"`
	}
	if err := json.Unmarshal(res, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Practices) != 1 || list.Practices[0].ID != id ||
		list.Practices[0].Phase != practicePhaseFinished {
		t.Fatalf("练习列表不正确: %s", res)
	}

	// 重练：另建记录，回到信号日，沿用原快照。
	res = doReq(t, h, http.MethodPost, "/api/practice/"+id+"/retry",
		map[string]any{"requestId": "flow-retry-1"}, http.StatusCreated)
	var rv2 practiceSessionView
	if err := json.Unmarshal(res, &rv2); err != nil {
		t.Fatal(err)
	}
	if rv2.ID == id || rv2.SourceSessionID != id || rv2.Phase != practicePhaseActive ||
		rv2.Revision != 1 || rv2.Stage != practiceStageWaiting ||
		len(rv2.Days) != 1 || rv2.Days[0].Close != 10 || rv2.CurrentDay != 0 {
		t.Fatalf("重练会话不正确: %+v", rv2)
	}

	// 同 requestId 重试：幂等返回同一条重练记录。
	res = doReq(t, h, http.MethodPost, "/api/practice/"+id+"/retry",
		map[string]any{"requestId": "flow-retry-1"}, http.StatusCreated)
	var rv3 practiceSessionView
	if err := json.Unmarshal(res, &rv3); err != nil {
		t.Fatal(err)
	}
	if rv3.ID != rv2.ID {
		t.Fatalf("同 requestId 重练应幂等: %s vs %s", rv3.ID, rv2.ID)
	}
}

// TestPracticeAPIErrors 请求错误映射：请求级校验 400、不存在 404、
// 版本/命令冲突与未结束 409、同日卖出 400。
func TestPracticeAPIErrors(t *testing.T) {
	s, h := newPracticeServerForTest(t)
	st, err := s.builderStore.Create(practiceTestSpec())
	if err != nil {
		t.Fatalf("创建策略: %v", err)
	}

	// 请求级校验（400）与策略不存在（404）、版本冲突（409）。
	doReq(t, h, http.MethodPost, "/api/practice", map[string]any{
		"strategyVersion": 1, "fromYear": 2024, "toYear": 2024, "requestId": "e0",
	}, http.StatusBadRequest)
	doReq(t, h, http.MethodPost, "/api/practice", map[string]any{
		"strategyId": "st_20990101T000000000Z_deadbeef", "strategyVersion": 1,
		"fromYear": 2024, "toYear": 2024, "requestId": "e1",
	}, http.StatusNotFound)
	doReq(t, h, http.MethodPost, "/api/practice", map[string]any{
		"strategyId": st.ID, "strategyVersion": 1,
		"fromYear": 1989, "toYear": 2024, "requestId": "e2",
	}, http.StatusBadRequest)
	doReq(t, h, http.MethodPost, "/api/practice", map[string]any{
		"strategyId": st.ID, "strategyVersion": 1,
		"fromYear": 2024, "toYear": 2024, "requestId": "",
	}, http.StatusBadRequest)
	doReq(t, h, http.MethodPost, "/api/practice", map[string]any{
		"strategyId": st.ID, "strategyVersion": 99,
		"fromYear": 2024, "toYear": 2024, "requestId": "e3",
	}, http.StatusConflict)

	// 合法格式但不存在的会话：读取与动作均 404。
	ghost := "practice_20240102T000000000Z_deadbeef"
	doReq(t, h, http.MethodGet, "/api/practice/"+ghost, nil, http.StatusNotFound)
	doReq(t, h, http.MethodPost, "/api/practice/"+ghost+"/actions",
		map[string]any{"action": "next", "commandId": "e-x", "expectedRevision": 1}, http.StatusNotFound)

	id := practiceStartAndAwait(t, h, st.ID, "e-req")

	// 版本冲突：期望修订不匹配（409）。
	doReq(t, h, http.MethodPost, "/api/practice/"+id+"/actions",
		map[string]any{"action": "next", "commandId": "e-c0", "expectedRevision": 9}, http.StatusConflict)

	// 非法动作（400）。
	doReq(t, h, http.MethodPost, "/api/practice/"+id+"/actions",
		map[string]any{"action": "noop", "commandId": "e-c1", "expectedRevision": 1}, http.StatusBadRequest)

	// 同 commandId 换动作：命令冲突（409）。
	doReq(t, h, http.MethodPost, "/api/practice/"+id+"/actions",
		map[string]any{"action": "next", "commandId": "e-c2", "expectedRevision": 1}, http.StatusOK)
	doReq(t, h, http.MethodPost, "/api/practice/"+id+"/actions",
		map[string]any{"action": "buy", "commandId": "e-c2", "expectedRevision": 2}, http.StatusConflict)

	// 买入后在同一天卖出：T+1 拒绝（400），状态不变。
	doReq(t, h, http.MethodPost, "/api/practice/"+id+"/actions",
		map[string]any{"action": "buy", "commandId": "e-c3", "expectedRevision": 2}, http.StatusOK)
	doReq(t, h, http.MethodPost, "/api/practice/"+id+"/actions",
		map[string]any{"action": "sell", "commandId": "e-c4", "expectedRevision": 3}, http.StatusBadRequest)

	// 未结束时复盘与重练：409；空 requestId：400。
	doReq(t, h, http.MethodGet, "/api/practice/"+id+"/review", nil, http.StatusConflict)
	doReq(t, h, http.MethodPost, "/api/practice/"+id+"/retry",
		map[string]any{"requestId": "e-r1"}, http.StatusConflict)
	doReq(t, h, http.MethodPost, "/api/practice/"+id+"/retry",
		map[string]any{"requestId": ""}, http.StatusBadRequest)
}

// TestPracticeAPISnapshotIsolation 快照隔离：修改策略名称或更换行情来源后，
// 旧题视图、复盘与重练仍使用开题快照，不受后续更新影响。
func TestPracticeAPISnapshotIsolation(t *testing.T) {
	s, h := newPracticeServerForTest(t)
	spec := practiceTestSpec()
	spec.Name = "快照隔离策略"
	st, err := s.builderStore.Create(spec)
	if err != nil {
		t.Fatalf("创建策略: %v", err)
	}
	id := practiceStartAndAwait(t, h, st.ID, "snap-req")

	// 未交易直接结束（finished_untraded）。
	v := practiceDoAction(t, h, id, "finish", "s-c1", 1)
	if v.Phase != practicePhaseFinished || v.EndReason != practiceEndFinishedUntraded {
		t.Fatalf("结束状态不正确: %+v", v)
	}

	// 修改策略名称（版本 1 → 2）。
	newSpec := practiceTestSpec()
	newSpec.Name = "改名后的策略"
	if _, err := s.builderStore.Update(st.ID, newSpec, 1); err != nil {
		t.Fatalf("更新策略: %v", err)
	}

	// 旧题视图与复盘仍用快照名称与原行情。
	res := doReq(t, h, http.MethodGet, "/api/practice/"+id, nil, http.StatusOK)
	var v2 practiceSessionView
	if err := json.Unmarshal(res, &v2); err != nil {
		t.Fatal(err)
	}
	if v2.StrategyName != "快照隔离策略" {
		t.Fatalf("旧题视图应使用开题快照: %q", v2.StrategyName)
	}
	res = doReq(t, h, http.MethodGet, "/api/practice/"+id+"/review", nil, http.StatusOK)
	var rv practiceReviewView
	if err := json.Unmarshal(res, &rv); err != nil {
		t.Fatal(err)
	}
	if rv.StrategyName != "快照隔离策略" || rv.Code != "sh600000" ||
		len(rv.Days) != 8 || rv.Days[0].Close != 10 {
		t.Fatalf("复盘应使用开题快照: %+v", rv)
	}

	// 重练沿用原策略快照。
	res = doReq(t, h, http.MethodPost, "/api/practice/"+id+"/retry",
		map[string]any{"requestId": "snap-retry-1"}, http.StatusCreated)
	var rv2 practiceSessionView
	if err := json.Unmarshal(res, &rv2); err != nil {
		t.Fatal(err)
	}
	if rv2.StrategyName != "快照隔离策略" || len(rv2.Days) != 1 || rv2.Days[0].Close != 10 {
		t.Fatalf("重练应使用开题快照: %+v", rv2)
	}

	// 更换行情来源（模拟行情更新）：重练不重新扫描，仍用原行情快照。
	s.runner.practiceSource = fakePracticeSource{codes: []string{}, klines: map[string]extend.Klines{}}
	res = doReq(t, h, http.MethodPost, "/api/practice/"+id+"/retry",
		map[string]any{"requestId": "snap-retry-2"}, http.StatusCreated)
	var rv3 practiceSessionView
	if err := json.Unmarshal(res, &rv3); err != nil {
		t.Fatal(err)
	}
	if rv3.StrategyName != "快照隔离策略" || len(rv3.Days) != 1 || rv3.Days[0].Close != 10 {
		t.Fatalf("行情更新后重练仍应使用原快照: %+v", rv3)
	}
}

func TestPracticePreparingResponseKeepsParametersAndTaskIdentity(t *testing.T) {
	s, _ := newPracticeServerForTest(t)
	sess, err := s.practiceStore.CreatePreparing("strategy", 1, practiceTestSpec(), practiceCostSnapshot(core.DefaultCost()), 2020, 2024, "req-progress")
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.writePracticeState(rec, sess, nil)
	var view map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view["fromYear"] != float64(2020) || view["toYear"] != float64(2024) || view["strategyName"] != practiceTestSpec().Name {
		t.Fatalf("view=%v", view)
	}
	for _, key := range []string{"code", "days", "signalIndex"} {
		if _, ok := view[key]; ok {
			t.Fatalf("准备阶段泄露 %s", key)
		}
	}
	s.runner.mu.Lock()
	s.runner.beginTask(taskStart{kind: practiceTaskPrepare, phase: practicePhaseKeyPrepare, runID: sess.ID})
	s.runner.mu.Unlock()
	if id := s.runner.Status()["runId"]; id != sess.ID {
		t.Fatalf("runId=%v", id)
	}
}

func TestPracticeReportOnlyAvailableAfterFinish(t *testing.T) {
	s, h := newPracticeServerForTest(t)
	sess, err := s.practiceStore.CreatePreparing("s", 1, practiceTestSpec(), practiceCostSnapshot(practiceTestCost), 2024, 2024, "report-http")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.practiceStore.Publish(sess.ID, practiceTestMarket(t, []float64{10, 11}), 0); err != nil {
		t.Fatal(err)
	}
	status, _ := practiceTryReq(t, h, http.MethodGet, "/api/practice/"+sess.ID+"/review", nil)
	if status != http.StatusConflict {
		t.Fatalf("未结束 report status=%d", status)
	}
	_, active := practiceTryReq(t, h, http.MethodGet, "/api/practice/"+sess.ID, nil)
	var state map[string]any
	if err = json.Unmarshal(active, &state); err != nil {
		t.Fatal(err)
	}
	if _, ok := state["report"]; ok {
		t.Fatal("盲练响应不能包含报告")
	}
	if _, _, err = s.practiceStore.Apply(sess.ID, practiceCommand{Action: practiceActionSkip, CommandID: "skip-report", ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	status, body := practiceTryReq(t, h, http.MethodGet, "/api/practice/"+sess.ID+"/review", nil)
	var review practiceReviewView
	if err = json.Unmarshal(body, &review); err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK || review.Report == nil || review.Report.Traded || review.Report.Equity != practiceBudgetYuan {
		t.Fatalf("status=%d report=%+v", status, review.Report)
	}
}
