package lab

import (
	"github.com/injoyai/strategy-tail/internal/lab/builder"
)

// replay.go 回放数据合同（docs/plans/2026-10-18-backtest-replay.md §2）。
//
// 完整每日数据由 runAccountBacktest 在原成交与收盘估值位置旁路采集，
// 独立保存于报告目录 replay/ 之下，不内嵌 report.json——列表操作
// 反序列化报告时不会加载回放明细。采集只读执行状态并追加事件，
// 不改变求值次数、短路顺序、成交与每日账户结果（R07/R15）。
//
// 仓位身份在一次买入时分配，卖出沿用；同日同票卖出再买入得到新身份。
// 成交事件按实际执行顺序记录，不按最终交易列表还原。

// replayVersion 回放数据格式版本；读取端遇到未知版本须明确报错。
const replayVersion = 1

// replayTimeLayout 回放事件与持仓帧的时刻格式（本地时区，无时区后缀）。
const replayTimeLayout = "2006-01-02 15:04:05"

// ReplayHolding 每日收盘帧中的单个持仓不可变快照。
// 总成本含买入费用；成本单价 = TotalCost/Quantity；估值价取当日收盘，
// 缺日线时沿用上次有效价格并在 MarkDate 披露价格日期。
type ReplayHolding struct {
	PositionID   int     `json:"positionId"`
	Code         string  `json:"code"`
	BuyTime      string  `json:"buyTime"` // 买入时刻（收盘 15:00）
	Quantity     int     `json:"quantity"`
	BuyPrice     float64 `json:"buyPrice"`     // 原始收盘价
	BuyExecPrice float64 `json:"buyExecPrice"` // 含滑点实际成交价
	TotalCost    float64 `json:"totalCost"`    // 含买入费用总成本
	CostPrice    float64 `json:"costPrice"`    // 成本单价
	MarkPrice    float64 `json:"markPrice"`    // 估值价
	MarkDate     string  `json:"markDate"`     // 估值价日期
	MarketValue  float64 `json:"marketValue"`
	FloatingPnL  float64 `json:"floatingPnl"`  // 市值 − 总成本
	FloatingRate float64 `json:"floatingRate"` // FloatingPnL/TotalCost×100
}

// ReplayTrade 实际成交事件。Fee 口径：买入 = BuyCost − ExecPrice×Quantity，
// 卖出 = ExecPrice×Quantity − SellIncome（滑点已体现在成交价，不重复计费）。
type ReplayTrade struct {
	Seq            int      `json:"seq"` // 变体内递增执行序号
	PositionID     int      `json:"positionId"`
	Code           string   `json:"code"`
	Time           string   `json:"time"`      // 实际成交时刻
	Side           string   `json:"side"`      // buy | sell
	Price          float64  `json:"price"`     // 策略可见原始价
	ExecPrice      float64  `json:"execPrice"` // 实际成交价（含滑点）
	Quantity       int      `json:"quantity"`
	Fee            float64  `json:"fee"`
	MinuteFallback bool     `json:"minuteFallback,omitempty"` // 分钟缺数降级
	Reason         []string `json:"reason,omitempty"`         // 真实成功路径（引用规则表 Path）
}

// ReplayDay 每日收盘帧，与账户点一一对应。
type ReplayDay struct {
	Date          string          `json:"date"`
	Equity        float64         `json:"equity"`
	Cash          float64         `json:"cash"`
	MarketValue   float64         `json:"marketValue"`
	PositionRatio float64         `json:"positionRatio"` // 持仓市值/总资产×100
	DayPnL        float64         `json:"dayPnl"`        // 当日总资产 − 上一交易日总资产；首日为 − 初始资金
	Holdings      []ReplayHolding `json:"holdings"`
	Trades        []ReplayTrade   `json:"trades,omitempty"` // 当日全部成交事件
}

// ReplayVariant 单变体完整回放数据（保存为 replay/variant-<index>.json）。
// 变体用报告原始序号识别，不使用界面排序位置或策略名称。
type ReplayVariant struct {
	Version      int                `json:"version"`
	VariantIndex int                `json:"variantIndex"`
	VariantName  string             `json:"variantName"`
	Dates        []string           `json:"dates"`
	Days         []ReplayDay        `json:"days"`
	Rules        []builder.RuleDesc `json:"rules,omitempty"`

	nextSeq int           // 成交事件递增序号（采集期使用，不序列化）
	pending []ReplayTrade // 当日已产生、待收盘帧收走的事件
}

// recordTrade 追加一条成交事件（执行顺序）。
func (rv *ReplayVariant) recordTrade(ev ReplayTrade) {
	rv.nextSeq++
	ev.Seq = rv.nextSeq
	rv.pending = append(rv.pending, ev)
}

// takeDayTrades 收盘帧取走当日事件；同日多笔按执行顺序完整保留。
func (rv *ReplayVariant) takeDayTrades() []ReplayTrade {
	if len(rv.pending) == 0 {
		return nil
	}
	out := rv.pending
	rv.pending = nil
	return out
}

// snapshotRules 冻结规则描述表（回测结束时调用一次，构建期已完整登记）。
func (rv *ReplayVariant) snapshotRules(rc *builder.RecordContext) {
	if rc == nil {
		return
	}
	rv.Rules = rc.Rules()
}
