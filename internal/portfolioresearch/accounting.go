// accounting.go 每日会计、公司行为与对账（v2 设计 §9.4/§9.5）。
//
// 会计口径（文档化）：
//   - 估值统一按执行日收盘价（ClosePrice，即已确认的复权/估值口径，输入给定）；
//     缺失时退回前收盘 PrevClose；再缺失按 0 估值并记录质量事件 + 降级（fail closed）。
//   - 期初权益 beginEquity = 期初现金 + Σ(期初持仓 × 前收盘)；
//     期末权益 endEquity = 期末现金 + Σ(期末持仓 × 收盘估值价)。
//   - 当日损益 pnl = 已实现 + 公司行为收入 + 未实现变化：
//   - 已实现（realized）= Σ 卖出毛额 - 卖出份额成本基础（成本基础不含费用）；
//   - 公司行为收入（corporateIncome）= 当日分红现金流入；
//   - 未实现变化（Δunrealized）= (期末市值 - 期末成本基础) - (期初市值 - 期初成本基础)。
//   - 费用 fees = 当日全部成交的佣金 + 印花税 + 过户费（独立减项）。
//   - 对账恒等式 beginEquity + pnl - fees = endEquity 由现金流 + 市值变化代换恒成立
//     （同一输入重复运行逐位一致，见 execution.go 确定性说明）。
//
// 公司行为（§9.5）：分红/拆并股/退市处理数据可靠时按本文件应用并记录事件；
// 数据不足（如退市缺可靠价格）必须产生质量事件并降低证据等级。
package portfolioresearch

import (
	"fmt"
	"math"
	"sort"
)

// reconcileTolerance 对账残差容差（元；浮点累积误差远小于该量级）。
const reconcileTolerance = 1e-6

// ---- 领域类型 ----

// CorporateAction 当日公司行为（Kind 使用 CorporateAction* 常量）。
type CorporateAction struct {
	Code     string  `json:"code"`
	Kind     string  `json:"kind"`               // dividend | split | delist
	PerShare float64 `json:"perShare,omitempty"` // dividend: 每股分红（元）；split: 拆分比例（股数×）
	Detail   string  `json:"detail,omitempty"`
}

// ExecutionQualityEvent 执行/会计层的数据质量或公司行为事件（证据降级标记进入
// 账本与报告）。区别于 transform.go 的 QualityEvent（截面变换层），语义独立。
type ExecutionQualityEvent struct {
	Date     string `json:"date"`
	Code     string `json:"code,omitempty"`
	Type     string `json:"type"` // missing_market_data | delisted | corporate_action | capacity
	Detail   string `json:"detail,omitempty"`
	Degraded bool   `json:"degraded"`
}

// DailyLedger 单日账本：逐笔意图/成交/拒绝 + 现金/持仓/净值 + 对账结果。
type DailyLedger struct {
	Date            string                  `json:"date"`
	BeginCash       float64                 `json:"beginCash"`
	EndCash         float64                 `json:"endCash"`
	Intents         []OrderIntent           `json:"intents"`
	Fills           []Fill                  `json:"fills"`
	Rejections      []Rejection             `json:"rejections"`
	BeginHoldings   []HoldingLot            `json:"beginHoldings"`
	EndHoldings     []HoldingLot            `json:"endHoldings"`
	BeginEquity     float64                 `json:"beginEquity"`
	EndEquity       float64                 `json:"endEquity"`
	RealizedPnL     float64                 `json:"realizedPnL"`
	CorporateIncome float64                 `json:"corporateIncome"`
	UnrealizedPnL   float64                 `json:"unrealizedPnL"`
	Pnl             float64                 `json:"pnl"`
	Fees            FeeBreakdown            `json:"fees"`
	QualityEvents   []ExecutionQualityEvent `json:"qualityEvents,omitempty"`
	Degraded        bool                    `json:"degraded"`
	Reconciled      bool                    `json:"reconciled"`
	ReconResidual   float64                 `json:"reconResidual"`
}

// ---- 估值与对账 ----

// MarketValue 持仓按估值价计算市值（缺失代码按 0；估值价必须 >0 才算有效）。
// 供报告/测试直接估值使用；执行引擎内部使用带质量事件记录的估值路径。
func MarketValue(lots []HoldingLot, marks map[string]float64) float64 {
	s := 0.0
	for _, l := range lots {
		if p := marks[l.Code]; p > 0 {
			s += float64(l.Shares) * p
		}
	}
	return s
}

// Reconcile 对账：beginEquity + pnl - fees ≈ endEquity（容差内一致）。
// 返回是否一致与残差（残差 = 左式 - 右式，应 ≈ 0）。
func Reconcile(beginEquity, pnl, fees, endEquity float64) (bool, float64) {
	residual := beginEquity + pnl - fees - endEquity
	return math.Abs(residual) <= reconcileTolerance, residual
}

// beginMark 期初估值价：前收盘 → 收盘 → 0（fail closed + 质量事件 + 降级）。
func (w *execWork) beginMark(code string) float64 {
	if p, ok := w.market.PrevClose[code]; ok && finitePositive(p) {
		return p
	}
	if p, ok := w.market.ClosePrice[code]; ok && finitePositive(p) {
		return p
	}
	w.markQuality("missing_market_data", code, "期初缺前收盘价（按 0 估值，fail closed）", true)
	return 0
}

// endMark 期末估值价：收盘 → 前收盘 → 0（fail closed + 质量事件 + 降级）。
func (w *execWork) endMark(code string) float64 {
	if p, ok := w.market.ClosePrice[code]; ok && finitePositive(p) {
		return p
	}
	if p, ok := w.market.PrevClose[code]; ok && finitePositive(p) {
		return p
	}
	w.markQuality("missing_market_data", code, "期末缺估值价（按 0 估值，fail closed）", true)
	return 0
}

// beginEquity 期初权益 = 现金 + Σ(期初持仓 × 前收盘)（公司行为前口径）。
func (w *execWork) beginEquity(cash float64, lots []HoldingLot) float64 {
	eq := cash
	for _, l := range lots {
		eq += float64(l.Shares) * w.beginMark(l.Code)
	}
	return eq
}

// endEquity 期末权益 = 现金 + Σ(期末持仓 × 收盘估值价)（统一口径）。
func (w *execWork) endEquity(cash float64, lots []HoldingLot) float64 {
	eq := cash
	for _, l := range lots {
		eq += float64(l.Shares) * w.endMark(l.Code)
	}
	return eq
}

// unrealizedChange 未实现变化 = (期末市值 - 期末成本基础) - (期初市值 - 期初成本基础)。
// 持仓未动时退化为纯价格变动；卖出/买入通过成本基础代换自然抵消（对账恒等式来源）。
func (w *execWork) unrealizedChange(beginLots, endLots []HoldingLot) float64 {
	beginUnreal := marketValueAt(beginLots, w.beginMark) - grossCost(beginLots)
	endUnreal := marketValueAt(endLots, w.endMark) - grossCost(endLots)
	return endUnreal - beginUnreal
}

// marketValueAt 按估值回调计算持仓市值。
func marketValueAt(lots []HoldingLot, markOf func(string) float64) float64 {
	s := 0.0
	for _, l := range lots {
		s += float64(l.Shares) * markOf(l.Code)
	}
	return s
}

// grossCost 持仓成本基础合计（不含费用）。
func grossCost(lots []HoldingLot) float64 {
	s := 0.0
	for _, l := range lots {
		s += l.CostBasis
	}
	return s
}

// finitePositive 有限且 >0 的估值价。
func finitePositive(p float64) bool {
	return !math.IsNaN(p) && !math.IsInf(p, 0) && p > 0
}

// ---- 公司行为 ----

// applyCorporateActions 应用当日公司行为（分红/拆并股/退市），记录质量事件。
// 目标股数必须与调整后的持仓同口径（复权/公司行为已确认，设计 §9.5）。
// 按代码升序处理（确定性，map 遍历序不参与）。
//
// 无指针副作用（PROJECT_RULES §7）：入参 lots/cash 按值传入，返回调整后的
// 持仓与现金，由调用方赋值回状态。
//
// 非法输入协议（可观测语义，fail closed，文档化）：PerShare 必须为有限且 >0
// 的正数（NaN/±Inf/<=0 均拒绝；注意 NaN 的 <=0 比较为 false，必须显式判有限）。
// 非法行为不应用（分红不加现金/不计收入、拆股不改持仓股数与买入价），产生
// corporate_action 质量事件（Detail 含 invalid_corporate_action 标识）并标记
// 当日 Degraded，不返回错误——调用方以账本质量事件与 Degraded 标记观测。
// 保证 NaN/Inf 永不进入现金、持仓股数与买入价。
func (w *execWork) applyCorporateActions(lots []HoldingLot, cash float64) ([]HoldingLot, float64) {
	codes := make([]string, 0, len(w.market.CorporateActions))
	for c := range w.market.CorporateActions {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	for _, code := range codes {
		actions := w.market.CorporateActions[code]
		sh := sharesOfCode(lots, code)
		for _, a := range actions {
			switch a.Kind {
			case CorporateActionDividend:
				if !finitePositive(a.PerShare) {
					w.rejectInvalidCorporateAction(code, "分红", a)
					continue
				}
				amt := float64(sh) * a.PerShare
				cash += amt
				w.dividendIncome += amt
				w.markQuality("corporate_action", code,
					fmt.Sprintf("分红 %.4f/股 × %d 股 = %.2f（%s）", a.PerShare, sh, amt, a.Detail), false)
			case CorporateActionSplit:
				if !finitePositive(a.PerShare) {
					w.rejectInvalidCorporateAction(code, "拆并股", a)
					continue
				}
				for i := range lots {
					l := &lots[i]
					if l.Code != code {
						continue
					}
					l.Shares = int(math.Round(float64(l.Shares) * a.PerShare))
					l.BuyPrice /= a.PerShare
					// 成本基础不变（每股成本按比例调整）。
				}
				w.markQuality("corporate_action", code,
					fmt.Sprintf("拆并股 ×%.4f（%s）", a.PerShare, a.Detail), false)
			case CorporateActionDelist:
				w.delisted[code] = true
				w.markQuality("delisted", code,
					fmt.Sprintf("退市处理：缺乏可靠退市价，按最后可用价估值并降级（%s）", a.Detail), true)
			}
		}
	}
	return lots, cash
}

// rejectInvalidCorporateAction 公司行为参数非法（PerShare 非有限或 <=0）：
// fail closed——不应用该行为，产生 corporate_action 质量事件（Detail 含
// invalid_corporate_action 标识，供上层/报告机器可识别）并标记当日降级。
func (w *execWork) rejectInvalidCorporateAction(code, kindLabel string, a CorporateAction) {
	w.markQuality("corporate_action", code,
		fmt.Sprintf("invalid_corporate_action：%s比例非法（PerShare=%v，必须有限且>0），跳过该行为并降级（%s）",
			kindLabel, a.PerShare, a.Detail), true)
}

// sharesOfCode 某代码持仓股数合计。
func sharesOfCode(lots []HoldingLot, code string) int {
	n := 0
	for _, l := range lots {
		if l.Code == code {
			n += l.Shares
		}
	}
	return n
}
