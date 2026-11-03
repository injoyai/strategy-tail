package lab

import (
	"errors"
	"log"
	"net/http"
)

// practice_server.go 盘感练习 HTTP 接口（实施文档 §4）。
//
// 信息隔离：活跃视图只含已揭示行情（相对信号日的第 N 日），不返回股票
// 代码、真实日期、Unix 时间戳、未来行情根数或数据截止日期；身份与真实
// 日期仅在结束后的复盘视图披露，复盘窗口为结束时可见位置之后最多
// practiceReviewExtraDays 个可用交易日（不暴露全量快照）。
// 旧响应不覆盖新状态：视图一律由服务端当前持久状态投影，客户端提交的
// 只有动作、命令幂等 ID 与期望版本。

// practiceKlineView 单根已揭示日 K（盲练不含真实日期；复盘补 Date）。
// 指标窗口不足为 null（*float64 无 omitempty，显式输出 null）。
type practiceKlineView struct {
	Day    int      `json:"day"` // 相对信号日：负＝背景，0＝信号日
	Date   string   `json:"date,omitempty"`
	Open   float64  `json:"open"`
	High   float64  `json:"high"`
	Low    float64  `json:"low"`
	Close  float64  `json:"close"`
	Volume int64    `json:"volume"`
	MA5    *float64 `json:"ma5"`
	MA10   *float64 `json:"ma10"`
	MA20   *float64 `json:"ma20"`
	DIF    *float64 `json:"dif"`
	DEA    *float64 `json:"dea"`
	HIST   *float64 `json:"hist"`
}

// practicePriceYuan 返回元值。
func practicePriceYuan(p float64) float64 { return p }

// practiceKlineViews 投影 [0, through] 范围的日 K（through 越界夹到最后）。
// withDate 仅复盘为 true；盲练不下发真实日期。
func practiceKlineViews(m *practiceMarket, signalIndex, through int, withDate bool) []practiceKlineView {
	if through >= len(m.Days) {
		through = len(m.Days) - 1
	}
	start := max(0, signalIndex-120)
	views := make([]practiceKlineView, 0, through-start+1)
	for i := start; i <= through; i++ {
		k := m.Days[i]
		v := practiceKlineView{
			Day:    i - signalIndex,
			Open:   practicePriceYuan(k.Open),
			High:   practicePriceYuan(k.High),
			Low:    practicePriceYuan(k.Low),
			Close:  practicePriceYuan(k.Close),
			Volume: k.Volume,
			MA5:    k.MA5, MA10: k.MA10, MA20: k.MA20,
			DIF: k.DIF, DEA: k.DEA, HIST: k.HIST,
		}
		if withDate {
			v.Date = k.Date
		}
		views = append(views, v)
	}
	return views
}

// practiceIntPtr 返回整数指针（可空日索引）。
func practiceIntPtr(v int) *int { return &v }

// practiceSessionView 活跃会话视图：状态、仓位、已揭示行情与可用操作。
type practiceSessionView struct {
	ID              string              `json:"id"`
	Phase           string              `json:"phase"`
	Stage           string              `json:"stage,omitempty"`
	StrategyID      string              `json:"strategyId"`
	StrategyName    string              `json:"strategyName"`
	FromYear        int                 `json:"fromYear"`
	ToYear          int                 `json:"toYear"`
	BudgetYuan      float64             `json:"budgetYuan"`
	Revision        int64               `json:"revision"`
	CurrentDay      int                 `json:"currentDay"`
	Quantity        int                 `json:"quantity"`
	BuyDay          *int                `json:"buyDay,omitempty"`
	BuyCostYuan     float64             `json:"buyCostYuan"`
	SellDay         *int                `json:"sellDay,omitempty"`
	SellIncomeYuan  float64             `json:"sellIncomeYuan"`
	EndReason       string              `json:"endReason,omitempty"`
	SourceSessionID string              `json:"sourceSessionId,omitempty"`
	Eval            practiceEval        `json:"eval"`
	Days            []practiceKlineView `json:"days"`
	CurrentPrice    float64             `json:"currentPrice"`
	CanNext         bool                `json:"canNext"`
	CanBuy          bool                `json:"canBuy"`
	CanSell         bool                `json:"canSell"`
	CanSkip         bool                `json:"canSkip"`
	CanFinish       bool                `json:"canFinish"`
}

// practiceSessionView 由持久状态投影活跃/已结束视图。m 为 nil（非
// active/finished）时不应调用本方法，走 failReason 简化视图。
func practiceSessionViewOf(sess *practiceSession, m *practiceMarket) practiceSessionView {
	last := len(m.Days) - 1
	v := practiceSessionView{
		ID:              sess.ID,
		Phase:           sess.Phase,
		Stage:           sess.Stage,
		StrategyID:      sess.StrategyID,
		StrategyName:    sess.StrategySpec.Name,
		FromYear:        sess.FromYear,
		ToYear:          sess.ToYear,
		BudgetYuan:      sess.BudgetYuan,
		Revision:        sess.Revision,
		CurrentDay:      sess.CurrentIndex - sess.SignalIndex,
		Quantity:        sess.Quantity,
		BuyCostYuan:     sess.BuyCostYuan,
		SellIncomeYuan:  sess.SellIncomeYuan,
		EndReason:       sess.EndReason,
		SourceSessionID: sess.SourceSessionID,
		Eval:            sess.evaluate(m),
		Days:            practiceKlineViews(m, sess.SignalIndex, sess.CurrentIndex, false),
	}
	if sess.BuyIndex >= 0 {
		v.BuyDay = practiceIntPtr(sess.BuyIndex - sess.SignalIndex)
	}
	if sess.SellIndex >= 0 {
		v.SellDay = practiceIntPtr(sess.SellIndex - sess.SignalIndex)
	}
	if day := m.dayAt(sess.CurrentIndex); day.Close > 0.0 {
		v.CurrentPrice = practicePriceYuan(day.Close)
	}
	active := sess.Phase == practicePhaseActive
	v.CanNext = active && sess.CurrentIndex+1 <= last
	v.CanBuy = active && sess.Stage == practiceStageWaiting
	v.CanSell = active && sess.Stage == practiceStageHolding && sess.CurrentIndex > sess.BuyIndex
	v.CanSkip = v.CanBuy
	v.CanFinish = active
	return v
}

// practiceTradeView 复盘成交明细。
type practiceTradeView struct {
	Day      int     `json:"day"`
	Date     string  `json:"date"`
	Action   string  `json:"action"`
	Price    float64 `json:"price"`
	Quantity int     `json:"quantity"`
	Amount   float64 `json:"amount"`
	Fee      float64 `json:"fee"`
}

// practiceReviewView 复盘视图：结束与撤销后才可见的身份、真实日期、
// 完整窗口与成交明细（需求 §6）。
type practiceReviewView struct {
	Report       *practiceReport       `json:"report,omitempty"`
	ID           string                `json:"id"`
	Code         string                `json:"code"`
	Source       string                `json:"source"`
	LoadedAt     string                `json:"loadedAt"`
	DataEndDate  string                `json:"dataEndDate"`
	StrategyName string                `json:"strategyName"`
	EndReason    string                `json:"endReason"`
	SignalIndex  int                   `json:"signalIndex"`
	ReviewEndDay int                   `json:"reviewEndDay"`
	ExtraDays    int                   `json:"extraDays"`
	Days         []practiceKlineView   `json:"days"`
	Trades       []practiceTradeView   `json:"trades,omitempty"`
	Coverage     *practiceScanCoverage `json:"coverage,omitempty"`
	Eval         practiceEval          `json:"eval"`
}

// practiceReviewViewOf 投影复盘视图。复盘上界＝结束时可见位置＋
// practiceReviewExtraDays，夹到快照末尾；不返回上界之外的数据。
func practiceReviewViewOf(sess *practiceSession, m *practiceMarket) practiceReviewView {
	last := len(m.Days) - 1
	reviewEnd := sess.CurrentIndex + practiceReviewExtraDays
	if reviewEnd > last {
		reviewEnd = last
	}
	v := practiceReviewView{
		ID:           sess.ID,
		Code:         m.Code,
		Source:       m.Source,
		LoadedAt:     m.LoadedAt,
		StrategyName: sess.StrategySpec.Name,
		EndReason:    sess.EndReason,
		SignalIndex:  sess.SignalIndex,
		ReviewEndDay: reviewEnd - sess.SignalIndex,
		ExtraDays:    reviewEnd - sess.CurrentIndex,
		Days:         practiceKlineViews(m, sess.SignalIndex, reviewEnd, true),
		Coverage:     m.Coverage,
		Eval:         sess.evaluate(m),
	}
	if last >= 0 {
		v.DataEndDate = m.Days[last].Date
	}
	if sess.BuyIndex >= 0 && sess.BuyIndex <= last {
		k := m.Days[sess.BuyIndex]
		v.Trades = append(v.Trades, practiceTradeView{
			Day: sess.BuyIndex - sess.SignalIndex, Date: k.Date, Action: practiceActionBuy,
			Price: practicePriceYuan(k.Close), Quantity: sess.Quantity, Amount: sess.BuyCostYuan,
			Fee: sess.BuyCostYuan - practicePriceYuan(k.Close)*float64(sess.Quantity),
		})
	}
	if sess.SellIndex >= 0 && sess.SellIndex <= last {
		k := m.Days[sess.SellIndex]
		v.Trades = append(v.Trades, practiceTradeView{
			Day: sess.SellIndex - sess.SignalIndex, Date: k.Date, Action: practiceActionSell,
			Price: practicePriceYuan(k.Close), Quantity: sess.Quantity, Amount: sess.SellIncomeYuan,
			Fee: practicePriceYuan(k.Close)*float64(sess.Quantity) - sess.SellIncomeYuan,
		})
	}
	v.Report = practiceReportOf(sess, m, v.Trades)
	return v
}

// isPracticeRequestErr 领域/请求错误：已脱敏，可原文返回客户端。
func isPracticeRequestErr(err error) bool {
	return errors.Is(err, errPracticeNotFound) ||
		errors.Is(err, errPracticeVersionConflict) ||
		errors.Is(err, errPracticeCommandMismatch) ||
		errors.Is(err, errPracticeNotFinished) ||
		errors.Is(err, errPracticeInvalidCommand) ||
		errors.Is(err, errPracticeBudgetShort) ||
		errors.Is(err, errPracticeSameDaySell) ||
		errors.Is(err, errPracticeCorrupt) ||
		errors.Is(err, errPracticeRequestIDRequired) ||
		errors.Is(err, errPracticeRequestConflict) ||
		errors.Is(err, errPracticeStrategyNotFound) ||
		errors.Is(err, errPracticeStrategyVersion) ||
		errors.Is(err, errPracticeBusy) ||
		errors.Is(err, errPracticeInvalidPrice)
}

// practiceWriteErr 错误映射（实施文档 §4）：
// 404 不存在；409 版本冲突／命令冲突／未结束／requestId 冲突／策略版本／任务忙；
// 400 非法动作／预算不足／当日卖出／requestId 缺失；
// 500 价格异常／数据损坏／策略读取／内部错误；其余请求级错误 400 原文。
func practiceWriteErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errPracticeNotFound), errors.Is(err, errPracticeStrategyNotFound):
		writeErr(w, http.StatusNotFound, err.Error())
	case errors.Is(err, errPracticeVersionConflict),
		errors.Is(err, errPracticeCommandMismatch),
		errors.Is(err, errPracticeNotFinished),
		errors.Is(err, errPracticeRequestConflict),
		errors.Is(err, errPracticeStrategyVersion),
		errors.Is(err, errPracticeBusy):
		writeErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, errPracticeInvalidCommand),
		errors.Is(err, errPracticeBudgetShort),
		errors.Is(err, errPracticeSameDaySell),
		errors.Is(err, errPracticeRequestIDRequired):
		writeErr(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, errPracticeInvalidPrice),
		errors.Is(err, errPracticeCorrupt),
		errors.Is(err, errPracticeStrategyRead),
		errors.Is(err, errPracticeInternal):
		writeErr(w, http.StatusInternalServerError, err.Error())
	default:
		// 年份范围、买入条件等请求级校验错误：文本面向用户，原文返回。
		writeErr(w, http.StatusBadRequest, err.Error())
	}
}

// practiceGetForRequest 读取会话用于响应。失败时已写响应，返回 false。
func (s *Server) practiceGetForRequest(w http.ResponseWriter, id string) (*practiceSession, *practiceMarket, bool) {
	sess, m, err := s.practiceStore.Get(id)
	if err != nil {
		if errors.Is(err, errPracticeNotFound) || errors.Is(err, errPracticeCorrupt) {
			practiceWriteErr(w, err)
		} else {
			log.Printf("[lab] 读取练习 %s 失败: %v", id, err)
			practiceWriteErr(w, errPracticeInternal)
		}
		return nil, nil, false
	}
	return sess, m, true
}

// practiceApplyForRequest 应用命令并读取应用后的状态。失败时已写响应。
func (s *Server) practiceApplyForRequest(w http.ResponseWriter, id string, cmd practiceCommand) (*practiceSession, *practiceMarket, bool) {
	if _, _, err := s.practiceStore.Apply(id, cmd); err != nil {
		if isPracticeRequestErr(err) {
			practiceWriteErr(w, err)
		} else {
			log.Printf("[lab] 练习 %s 命令执行失败: %v", id, err)
			practiceWriteErr(w, errPracticeInternal)
		}
		return nil, nil, false
	}
	return s.practiceGetForRequest(w, id)
}

// writePracticeState 输出会话状态：active/finished 完整视图；其余阶段
// 包含出题参数及失败原因（无行情可投影，不暴露股票身份）。
func (s *Server) writePracticeState(w http.ResponseWriter, sess *practiceSession, m *practiceMarket) {
	if m != nil {
		writeJSON(w, practiceSessionViewOf(sess, m))
		return
	}
	writeJSON(w, map[string]any{"id": sess.ID, "phase": sess.Phase, "failReason": sess.FailReason,
		"strategyName": sess.StrategySpec.Name, "fromYear": sess.FromYear, "toYear": sess.ToYear,
		"revision": sess.Revision})
}

// handlePracticeStart POST /api/practice：受理出题请求（后台任务）。
func (s *Server) handlePracticeStart(w http.ResponseWriter, r *http.Request) {
	if !requireSameOrigin(w, r) {
		return
	}
	var req practiceStartRequest
	if !decodeStrict(r, w, &req, maxCandidateBody) {
		return
	}
	res, err := s.runner.StartPractice(req)
	if err != nil {
		practiceWriteErr(w, err)
		return
	}
	writeJSONCode(w, http.StatusAccepted, res)
}

// handlePracticeList GET /api/practices：练习历史摘要列表。
func (s *Server) handlePracticeList(w http.ResponseWriter, r *http.Request) {
	list, err := s.practiceStore.List()
	if err != nil {
		log.Printf("[lab] 读取练习列表失败: %v", err)
		writeErr(w, http.StatusInternalServerError, "读取练习列表失败")
		return
	}
	writeJSON(w, map[string]any{"practices": list})
}

// handlePracticeGet GET /api/practice/{id}：当前会话状态（按阶段截断）。
func (s *Server) handlePracticeGet(w http.ResponseWriter, r *http.Request) {
	sess, m, ok := s.practiceGetForRequest(w, r.PathValue("id"))
	if !ok {
		return
	}
	s.writePracticeState(w, sess, m)
}

// handlePracticeAction POST /api/practice/{id}/actions：提交练习命令。
func (s *Server) handlePracticeAction(w http.ResponseWriter, r *http.Request) {
	if !requireSameOrigin(w, r) {
		return
	}
	var cmd practiceCommand
	if !decodeStrict(r, w, &cmd, maxCandidateBody) {
		return
	}
	sess, m, ok := s.practiceApplyForRequest(w, r.PathValue("id"), cmd)
	if !ok {
		return
	}
	s.writePracticeState(w, sess, m)
}

// handlePracticeReview GET /api/practice/{id}/review：复盘（须已结束）。
func (s *Server) handlePracticeReview(w http.ResponseWriter, r *http.Request) {
	sess, m, ok := s.practiceGetForRequest(w, r.PathValue("id"))
	if !ok {
		return
	}
	if sess.Phase != practicePhaseFinished || m == nil {
		practiceWriteErr(w, errPracticeNotFinished)
		return
	}
	writeJSON(w, practiceReviewViewOf(sess, m))
}

// handlePracticeRetry POST /api/practice/{id}/retry：同题重练（另建记录）。
func (s *Server) handlePracticeRetry(w http.ResponseWriter, r *http.Request) {
	if !requireSameOrigin(w, r) {
		return
	}
	var req struct {
		RequestID string `json:"requestId"`
	}
	if !decodeStrict(r, w, &req, maxCandidateBody) {
		return
	}
	sess, err := s.practiceStore.Retry(r.PathValue("id"), req.RequestID)
	if err != nil {
		if isPracticeRequestErr(err) {
			practiceWriteErr(w, err)
		} else {
			log.Printf("[lab] 练习 %s 重练失败: %v", r.PathValue("id"), err)
			practiceWriteErr(w, errPracticeInternal)
		}
		return
	}
	newSess, m, ok := s.practiceGetForRequest(w, sess.ID)
	if !ok {
		return
	}
	writeJSONCode(w, http.StatusCreated, practiceSessionViewOf(newSess, m))
}
