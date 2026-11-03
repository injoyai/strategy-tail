package lab

import (
	"errors"
	"fmt"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/lab/builder"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/util"
)

// practice.go 盘感练习：会话状态机、成交与评价计算。
//
// 业务规则与评价口径见 docs/specs/2026-10-18-trading-practice-requirements.md，
// 落地方案见 docs/plans/2026-10-18-trading-practice.md。要点：
//
//   - 客户端只提交动作，不提交价格、股数、目标日期、代码或盈亏；
//     成交价一律取当前已揭示交易日收盘价，滑点为零。
//   - 每题独立预算 practiceBudgetYuan 元，买入数量为含费总支出不超预算的
//     最大 100 股整数倍；一次买入、一次全部卖出，买入当日不能卖出。
//   - 信号日为相对第 0 日，之前的背景为负索引；手动逐日推进，
//     最后一根行情仍允许当日决策，再推进或主动结束才收口。
//   - 未平仓只估值不虚构卖出；已实现收益只在真实卖出后产生。
//
// 价格单位约定：行情与费用使用浮点元值；
// 成交金额、预算与评价结果为元（float64）。

// practiceFormatV1 练习落盘格式版本；读取时不认识即拒绝，不猜测。
const practiceFormatV1 = 1

// practiceBudgetYuan 每题独立预算（元），含买入费用。
const practiceBudgetYuan = 20000.0

// practiceBudgetEpsilon 预算比较容差：费用为浮点计算，只抵消舍入误差。
const practiceBudgetEpsilon = 1e-6

// practiceReviewExtraDays 复盘窗口：结束后额外展示的可用交易日上限（需求 §6）。
// 仅约束复盘展示范围，不限制卖出后的逐日观察期。
const practiceReviewExtraDays = 20

// 练习生命周期阶段。preparing 为出题准备（后台任务执行中）；
// 重启后无执行者的 preparing 记录由 Store 恢复逻辑标为 failed。
const (
	practicePhasePreparing = "preparing"
	practicePhaseActive    = "active"
	practicePhaseFinished  = "finished"
	practicePhaseFailed    = "failed"
	practicePhaseCancelled = "cancelled"
)

// active 阶段内的仓位状态。
const (
	practiceStageWaiting = "waiting"
	practiceStageHolding = "holding"
	practiceStageSold    = "sold"
)

// 结束原因。跳过、行情用尽与主动结束分别记录；主动结束按结束时的
// 仓位状态细分为未交易／未平仓／已卖出，复盘标注不得相互冒充。
const (
	practiceEndSkipped          = "skipped"
	practiceEndDataEnd          = "data_end"
	practiceEndFinishedUntraded = "finished_untraded"
	practiceEndFinishedOpen     = "finished_open"
	practiceEndFinishedSold     = "finished_sold"
)

// 客户端可提交的动作。
const (
	practiceActionNext   = "next"
	practiceActionBuy    = "buy"
	practiceActionSell   = "sell"
	practiceActionSkip   = "skip"
	practiceActionFinish = "finish"
)

// 出题失败／中断原因（存 session 供列表展示；不含股票身份）。
const (
	practiceFailNoMatch   = "no_match"    // 范围内无命中机会
	practiceFailStrategy  = "strategy"    // 策略读取或买入条件无效
	practiceFailData      = "data"        // 本地行情缺失或损坏
	practiceFailInterrupt = "interrupted" // 服务重启导致准备中断
	practiceFailInternal  = "internal"    // 存储等其他内部错误
)

// practiceCommand 客户端练习命令。CommandID 用于重试幂等：
// 相同 ID 与动作重试返回权威状态，不重复执行；同 ID 换动作拒绝。
type practiceCommand struct {
	Action           string `json:"action"`
	CommandID        string `json:"commandId"`
	ExpectedRevision int64  `json:"expectedRevision"`
}

// practiceDayKline 快照内一根日 K。价格使用浮点元值，
// 对外投影按已揭示范围截断。
// 指标窗口不足时为 nil（JSON null），不写 NaN／Inf。
type practiceDayKline struct {
	Unix   int64    `json:"unix"` // 真实交易日 Unix 秒；私有，不下发
	Date   string   `json:"date"` // 真实日期（K 线自身时区格式化）；私有，复盘前不下发
	Open   float64  `json:"open"`
	High   float64  `json:"high"`
	Low    float64  `json:"low"`
	Close  float64  `json:"close"`
	Volume int64    `json:"volume"`
	MA5    *float64 `json:"ma5"` // 元
	MA10   *float64 `json:"ma10"`
	MA20   *float64 `json:"ma20"`
	DIF    *float64 `json:"dif"` // 元；首根为 0（EMA 初始口径同 extend.MACD）
	DEA    *float64 `json:"dea"`
	HIST   *float64 `json:"hist"` // DIF−DEA，不乘 2（需求 §4.1）
}

// practiceMarket 练习私有行情快照（market.json）。开题时一次写入，
// 之后不可变；后续交易、复盘、重练只读本快照，不再访问行情库。
type practiceMarket struct {
	PriceUnit string                `json:"priceUnit"`
	Version   int                   `json:"version"`
	Code      string                `json:"code"` // 私有：复盘前不得出现在任何响应
	Source    string                `json:"source"`
	LoadedAt  string                `json:"loadedAt"`           // UTC RFC3339
	Days      []practiceDayKline    `json:"days"`               // 完整快照：预热背景＋信号日＋之后全部可用日线
	Coverage  *practiceScanCoverage `json:"coverage,omitempty"` // 出题扫描覆盖（含失败明细）；仅复盘披露
}

// practiceSession 练习会话（session.json）。每条命令在 Store 锁内读取、
// 变更并原子写回；Revision 递增实现乐观并发，冲突方重新读取不自动重放。
type practiceSession struct {
	PriceUnit string `json:"priceUnit"`
	Version   int    `json:"version"`
	ID        string `json:"id"`
	Revision  int64  `json:"revision"`

	Phase string `json:"phase"` // preparing/active/finished/failed/cancelled
	Stage string `json:"stage"` // 仅 active：waiting/holding/sold

	// 开题快照：策略来源与完整定义、费用副本（Slippage=0）、预算与年份范围。
	// 后续修改策略库、费用配置或行情不影响已开始与重练的题目。
	StrategyID      string              `json:"strategyId"`
	StrategyVersion int                 `json:"strategyVersion"`
	StrategySpec    builder.BuilderSpec `json:"strategySpec"`
	Cost            core.Cost           `json:"cost"`
	BudgetYuan      float64             `json:"budgetYuan"`
	FromYear        int                 `json:"fromYear"`
	ToYear          int                 `json:"toYear"`

	// 出题请求幂等键与准备结果。
	RequestID  string `json:"requestId"`
	FailReason string `json:"failReason,omitempty"` // preparing 失败／取消原因码
	CreatedAt  string `json:"createdAt"`            // UTC RFC3339
	UpdatedAt  string `json:"updatedAt"`

	// 练习状态（抽题发布后有效）。信号日为 0 点，背景为负索引。
	SignalIndex  int `json:"signalIndex"`
	CurrentIndex int `json:"currentIndex"`

	// 成交记录。Quantity=0 表示未买入；SellIndex=-1 表示未卖出。
	Quantity       int     `json:"quantity"`
	BuyIndex       int     `json:"buyIndex"`
	BuyCostYuan    float64 `json:"buyCostYuan"` // 含费总成本（元）
	SellIndex      int     `json:"sellIndex"`
	SellIncomeYuan float64 `json:"sellIncomeYuan"` // 含费净收入（元）

	EndReason string `json:"endReason,omitempty"` // 仅 finished

	// 命令幂等：最近一次已执行命令。
	LastCommandID     string `json:"lastCommandId,omitempty"`
	LastCommandAction string `json:"lastCommandAction,omitempty"`

	// 重练关联与重练请求幂等（retry 以原会话上的 requestID 去重）。
	SourceSessionID    string `json:"sourceSessionId,omitempty"`
	LastRetryRequestID string `json:"lastRetryRequestId,omitempty"`
	LastRetrySessionID string `json:"lastRetrySessionId,omitempty"`
}

// 练习领域错误。HTTP 状态码映射见 practice_server.go；
// 错误文本不携带股票身份、真实日期或私有路径。
var (
	errPracticeNotFound        = errors.New("练习记录不存在")
	errPracticeVersionConflict = errors.New("练习状态已更新，请刷新后重试")
	errPracticeInvalidCommand  = errors.New("非法练习操作")
	errPracticeCommandMismatch = errors.New("命令 ID 已用于其他操作，请刷新后重试")
	errPracticeInvalidPrice    = errors.New("当前行情价格无效，无法成交")
	errPracticeBudgetShort     = errors.New("预算不足一手（含费用），无法买入")
	errPracticeSameDaySell     = errors.New("买入当天不能卖出，请先推进到下一交易日")
	errPracticeNotFinished     = errors.New("练习未结束，暂无复盘")
	errPracticeCorrupt         = errors.New("练习数据损坏")
)

// practiceCostSnapshot 练习费用快照：冻结调用时刻费率并将滑点清零
// （需求 §5：按当前可见交易日收盘价成交，不加滑点）。
func practiceCostSnapshot(c core.Cost) core.Cost {
	c.Slippage = 0.0
	return c
}

// practiceAffordableQuantity 计算含费预算内可买入的最大 100 股整数倍数量。
// 从不含费上界按整手向下调整，费用按 core.Cost 同一口径（Cost.BuyCost）计算。
// 返回 0 表示含费不足一手；返回值同时给出实际含费总支出（元）。
func practiceAffordableQuantity(c core.Cost, rawPrice float64, budgetYuan float64) (int, float64) {
	if rawPrice <= 0.0 || budgetYuan <= 0 {
		return 0, 0
	}
	upper := int(budgetYuan / rawPrice) // 不含费上界（股）
	for qty := upper / core.SharesPerLot * core.SharesPerLot; qty >= core.SharesPerLot; qty -= core.SharesPerLot {
		_, total := c.BuyCost(rawPrice, qty)
		if total <= budgetYuan+practiceBudgetEpsilon {
			return qty, total
		}
	}
	return 0, 0
}

// applyCommand 在会话上应用练习命令。返回是否产生了新的状态变更：
// 幂等重放（同 commandId 同动作）返回 false 且不改状态，调用方直接以
// 当前持久状态作为权威结果返回。状态不合法返回领域错误且不修改会话。
func (s *practiceSession) applyCommand(m *practiceMarket, cmd practiceCommand) (bool, error) {
	if cmd.CommandID == "" {
		return false, errPracticeInvalidCommand
	}
	if s.LastCommandID != "" && cmd.CommandID == s.LastCommandID {
		if cmd.Action != s.LastCommandAction {
			return false, errPracticeCommandMismatch
		}
		return false, nil
	}
	if cmd.ExpectedRevision != s.Revision {
		return false, errPracticeVersionConflict
	}
	if s.Phase != practicePhaseActive || m == nil {
		return false, errPracticeInvalidCommand
	}

	switch cmd.Action {
	case practiceActionNext:
		// 有下一根则推进；没有则以“行情用尽”结束，不虚构卖出。
		if s.CurrentIndex+1 >= len(m.Days) {
			s.finishWith(practiceEndDataEnd)
		} else {
			s.CurrentIndex++
		}

	case practiceActionBuy:
		if s.Stage != practiceStageWaiting {
			return false, errPracticeInvalidCommand
		}
		day := m.dayAt(s.CurrentIndex)
		if day.Close <= 0.0 {
			return false, errPracticeInvalidPrice
		}
		qty, total := practiceAffordableQuantity(s.Cost, day.Close, s.BudgetYuan)
		if qty == 0 {
			return false, errPracticeBudgetShort
		}
		s.Quantity = qty
		s.BuyIndex = s.CurrentIndex
		s.BuyCostYuan = total
		s.Stage = practiceStageHolding

	case practiceActionSell:
		if s.Stage != practiceStageHolding {
			return false, errPracticeInvalidCommand
		}
		if s.CurrentIndex == s.BuyIndex {
			return false, errPracticeSameDaySell
		}
		day := m.dayAt(s.CurrentIndex)
		if day.Close <= 0.0 {
			return false, errPracticeInvalidPrice
		}
		_, net := s.Cost.SellIncome(day.Close, s.Quantity)
		s.SellIndex = s.CurrentIndex
		s.SellIncomeYuan = net
		s.Stage = practiceStageSold

	case practiceActionSkip:
		// 仅未买入可跳过；跳过即结束本题，之后可查看复盘。
		if s.Stage != practiceStageWaiting {
			return false, errPracticeInvalidCommand
		}
		s.finishWith(practiceEndSkipped)

	case practiceActionFinish:
		// 任一 active 阶段可主动结束，按真实仓位状态记录结果。
		switch s.Stage {
		case practiceStageWaiting:
			s.finishWith(practiceEndFinishedUntraded)
		case practiceStageHolding:
			s.finishWith(practiceEndFinishedOpen)
		case practiceStageSold:
			s.finishWith(practiceEndFinishedSold)
		default:
			return false, errPracticeCorrupt
		}

	default:
		return false, errPracticeInvalidCommand
	}

	s.Revision++
	s.LastCommandID = cmd.CommandID
	s.LastCommandAction = cmd.Action
	return true, nil
}

// finishWith 收口会话并记录结束原因。
func (s *practiceSession) finishWith(reason string) {
	s.EndReason = reason
	s.Phase = practicePhaseFinished
}

// dayAt 返回索引处日 K；越界视为数据损坏（fail closed）。
func (m *practiceMarket) dayAt(i int) practiceDayKline {
	if i < 0 || i >= len(m.Days) {
		// 调用方已在状态机内保证索引有效；此处兜底返回零值，
		// 使后续价格检查（Close<=0）自然拒绝成交。
		return practiceDayKline{}
	}
	return m.Days[i]
}

// practiceEval 练习评价（需求 §5 口径）。未买入时各成交类指标为零，
// 是否“未交易”由调用方按 BuyIndex<0 判定，不当成零收益成交。
type practiceEval struct {
	RealizedPnL       float64 `json:"realizedPnl"`       // 已实现净盈亏（元）
	RealizedPct       float64 `json:"realizedPct"`       // 已实现净收益率（%）
	HoldingDays       int     `json:"holdingDays"`       // 持有交易日数（索引差）
	MaxFloatProfit    float64 `json:"maxFloatProfit"`    // 最大浮盈（元，收盘口径）
	MaxFloatProfitPct float64 `json:"maxFloatProfitPct"` // 最大浮盈收益率（%）
	MaxFloatLoss      float64 `json:"maxFloatLoss"`      // 最大浮亏（元，≤0）
	MaxFloatLossPct   float64 `json:"maxFloatLossPct"`   // 最大浮亏收益率（%）
	OpenPnL           float64 `json:"openPnl"`           // 未平仓浮动盈亏（元，不预扣卖出费）
	OpenPct           float64 `json:"openPct"`           // 未平仓浮动收益率（%）
}

// evaluate 计算当前（或终态）评价。
// 浮动盈亏按逐日收盘极值统计，包含买入日与卖出／结束日两端；
// 已卖出后固定截止于卖出日，此后涨跌不再改变持仓指标与已实现收益。
func (s *practiceSession) evaluate(m *practiceMarket) practiceEval {
	var ev practiceEval
	if s.Quantity <= 0 || s.BuyIndex < 0 || m == nil {
		return ev
	}
	cost := s.BuyCostYuan
	// 持有与浮盈亏统计的截止索引：卖出后固定于卖出日，否则为当前日。
	endIdx := s.CurrentIndex
	if s.Stage == practiceStageSold {
		endIdx = s.SellIndex
	}
	ev.HoldingDays = endIdx - s.BuyIndex
	if cost <= 0 {
		// 买入成本无效时不再外推比率，避免输出无穷（fail closed）。
		return ev
	}
	for i := s.BuyIndex; i <= endIdx && i < len(m.Days); i++ {
		pnl := m.Days[i].Close*float64(s.Quantity) - cost
		pct := pnl / cost * 100
		if pnl > ev.MaxFloatProfit {
			ev.MaxFloatProfit, ev.MaxFloatProfitPct = pnl, pct
		}
		if pnl < ev.MaxFloatLoss {
			ev.MaxFloatLoss, ev.MaxFloatLossPct = pnl, pct
		}
		if i == endIdx && s.Stage != practiceStageSold {
			ev.OpenPnL, ev.OpenPct = pnl, pct
		}
	}
	if s.Stage == practiceStageSold {
		ev.RealizedPnL = s.SellIncomeYuan - cost
		ev.RealizedPct = ev.RealizedPnL / cost * 100
	}
	return ev
}

// buildPracticeMarket 由一次加载的完整日线构建不可变行情快照。
// 指标按完整历史因果计算后写入；MA 保留价格整数口径，MACD 复用策略公共算法。
// 不按可视窗口重置预热，窗口不足输出 null。
// 校验日期严格递增与收盘价有效，损坏数据拒绝出题。
func buildPracticeMarket(code string, ks extend.Klines, source, loadedAt string) (*practiceMarket, error) {
	if len(ks) == 0 {
		return nil, fmt.Errorf("行情为空")
	}
	prev := int64(-1)
	for i, k := range ks {
		if k == nil || k.Kline == nil {
			return nil, fmt.Errorf("第 %d 根日K为空", i+1)
		}
		if k.Close <= 0.0 {
			return nil, fmt.Errorf("第 %d 根日K收盘价无效", i+1)
		}
		if i > 0 && k.Unix <= prev {
			return nil, fmt.Errorf("日K日期未按升序排列")
		}
		prev = k.Unix
	}

	m := &practiceMarket{
		Version:  practiceFormatV1,
		Code:     code,
		Source:   source,
		LoadedAt: loadedAt,
		Days:     make([]practiceDayKline, len(ks)),
	}
	// MA 浮点滚动和（元，与 extend.Klines.MA 一致）。
	var sum5, sum10, sum20 float64
	difs := util.MACDDIF(ks, 12, 26)
	hists := util.MACDHistogram(ks, 12, 26, 9)
	for i, k := range ks {
		sum5 += k.Close
		sum10 += k.Close
		sum20 += k.Close
		if i >= 5 {
			sum5 -= ks[i-5].Close
		}
		if i >= 10 {
			sum10 -= ks[i-10].Close
		}
		if i >= 20 {
			sum20 -= ks[i-20].Close
		}

		d := &m.Days[i]
		d.Unix = k.Unix
		d.Date = k.Time.Format(time.DateOnly)
		d.Open, d.High, d.Low, d.Close, d.Volume = k.Open, k.High, k.Low, k.Close, k.Volume
		if i >= 4 {
			d.MA5 = yuanPtr(sum5 / 5)
		}
		if i >= 9 {
			d.MA10 = yuanPtr(sum10 / 10)
		}
		if i >= 19 {
			d.MA20 = yuanPtr(sum20 / 20)
		}
		d.DIF = yuanPtr(difs[i])
		d.DEA = yuanPtr(difs[i] - hists[i])
		d.HIST = yuanPtr(hists[i])
	}
	return m, nil
}

// yuanPtr 返回浮点元指针。
func yuanPtr(v float64) *float64 { return &v }

// practiceNowUTC 返回 UTC RFC3339 时间戳（落盘与展示统一口径）。
func practiceNowUTC() string { return time.Now().UTC().Format(time.RFC3339) }
