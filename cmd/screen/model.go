package main

import (
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

const (
	TypeBuy     = core.TypeBuy
	TypeSell    = core.TypeSell
	TypeHistory = "history"
)

// =========================================================
// 策略定义
// =========================================================

// Strategy 可切换的策略卡片，包含买入/卖出/辅助标签
type Strategy struct {
	Key    string                // 唯一标识，如 "macd-premium"
	Name   string                // 显示名，如 "MACD精选"
	Buyer  core.Buyer            // 买入策略
	Seller core.Seller           // 卖出策略
	Tags   map[string]core.Buyer // 辅助标签(板块/市值等)
}

func (this Strategy) checkTags(code string, ks extend.Klines) []string {
	ls := []string(nil)
	for k, v := range this.Tags {
		if v != nil {
			if v.Buy(code, ks) {
				ls = append(ls, k)
			}
		}
	}
	return ls
}

// =========================================================
// 响应数据结构
// =========================================================

// BuyItem - 买入信号条目
type BuyItem struct {
	Code       string   `json:"code"`        // 股票代码（带前缀）
	Name       string   `json:"name"`        // 股票名称
	Date       string   `json:"date"`        // 日期 YYYY-MM-DD
	Time       string   `json:"time"`        // 信号产生时间
	Price      float64  `json:"price"`       // 买入价
	Rise       float64  `json:"rise"`        // 盘中涨幅百分比（仅当日买点有效）
	CurrPrice  float64  `json:"curr_price"`  // 现价
	IncomeRate float64  `json:"income_rate"` // 收益率百分比
	Sold       bool     `json:"sold"`        // 是否已卖出
	SellPrice  float64  `json:"sell_price"`  // 卖出价（已卖出时有效）
	SellTime   string   `json:"sell_time"`   // 卖出时间（已卖出时有效）
	Strategy   string   `json:"strategy"`    // 命中的策略 key 列表
	Tags       []string `json:"tags"`        // 满足的辅助标签
}

func (this *BuyItem) AddLast(k *protocol.Kline) {
	if k != nil && !this.Sold {
		this.CurrPrice = k.Close.Float64()
		if this.Price != 0 {
			this.IncomeRate = (this.CurrPrice - this.Price) / this.Price * 100
		}
	}
}

// BuyResponse - 买点响应
type BuyResponse struct {
	Type    string    `json:"type"`    // 固定 "buy"
	Count   int       `json:"count"`   // 数量
	Time    string    `json:"time"`    // 刷新时间
	Results []BuyItem `json:"results"` // 列表
}

// Trade - 交易数据
type Trade struct {
	ID        int64    `json:"id"`                    //唯一标识
	Code      string   `json:"code"`                  // 股票代码
	Name      string   `json:"name"`                  // 股票名称
	BuyTime   string   `json:"buy_time"`              // 买入时间
	BuyPrice  float64  `json:"buy_price"`             // 买入价
	Sold      bool     `json:"sold" xorm:"index"`     //是否卖出
	SellTime  string   `json:"sell_time"`             // 卖出时间
	SellPrice float64  `json:"sell_price"`            // 卖出价
	Income    float64  `json:"income"`                // 收益
	Strategy  string   `json:"strategy" xorm:"index"` // 命中策略
	Tags      []string `json:"tags" xorm:"text json"` //满足的标签
}

func (this *Trade) Realtime(k *protocol.Kline) {
	if k != nil && !this.Sold {
		this.SellPrice = k.Close.Float64()
		if this.BuyPrice != 0 {
			this.Income = (this.SellPrice - this.BuyPrice) / this.BuyPrice * 100
		}
	}
}

func (this *Trade) Sell(s *core.Sell) *Trade {
	if s == nil {
		return this
	}
	this.Sold = true
	this.SellTime = s.Time.Format(time.DateTime)
	this.SellPrice = s.Price.Float64()
	if s.Price > 0 {
		this.Income = (this.SellPrice - this.BuyPrice) / this.SellPrice * 100
	}
	return this
}

func (this *Trade) ToBuy() (core.Buy, error) {
	t, err := time.Parse(time.DateTime, this.BuyTime)
	return core.Buy{
		Code:  this.Code,
		Time:  t,
		Price: protocol.Yuan(this.BuyPrice),
	}, err
}

func (this *Trade) ToSell() (*core.Sell, error) {
	t, err := time.Parse(time.DateTime, this.BuyTime)
	return &core.Sell{
		Code:  this.Code,
		Time:  t,
		Price: protocol.Yuan(this.SellPrice),
	}, err
}

// SellResponse - 卖点响应
type SellResponse struct {
	Type    string  `json:"type"`    // 固定 "sell"
	Count   int     `json:"count"`   // 数量
	Time    string  `json:"time"`    // 刷新时间
	Results []Trade `json:"results"` // 列表
}

// sellSignal 卖点信号载荷(包装[]*Trade用于marshal类型区分,避免按Code匹配的歧义)
type sellSignal []*Trade

// HistoryResponse - 历史买点响应（扁平结构，按时间倒序）
type HistoryResponse struct {
	Type    string   `json:"type"`    // 固定 "history"
	Time    string   `json:"time"`    // 刷新时间
	Total   int      `json:"total"`   // 总买点数量
	Results []*Trade `json:"results"` // 所有历史买点，按时间倒序
}

// =========================================================
// 诊断响应结构
// =========================================================

// ChartKline K线图数据条目
type ChartKline struct {
	Time   string  `json:"time"`
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume int64   `json:"volume"`
}

// DiagnoseResponse 诊断接口响应，参考 visualize 的 chartData
type DiagnoseResponse struct {
	Code        string              `json:"code"`
	Name        string              `json:"name"`
	Strategy    string              `json:"strategy"`
	Matched     bool                `json:"matched"`
	Klines      []ChartKline        `json:"klines"`
	Annotations []core.Annotation   `json:"annotations"`
	Explain     []core.ExplainStep  `json:"explain"`
	Diagnosis   core.DiagnoseResult `json:"diagnosis"`
	Trades      []DiagnoseTrade     `json:"trades"` // 该股票的历史成交记录
}

// DiagnoseTrade 诊断页单笔交易记录
type DiagnoseTrade struct {
	BuyTime    string  `json:"buy_time"`
	BuyPrice   float64 `json:"buy_price"`
	SellTime   string  `json:"sell_time"`
	SellPrice  float64 `json:"sell_price"`
	CurrPrice  float64 `json:"curr_price"`  // 现价(持仓中为最新收盘价,已卖出为卖出价)
	ProfitRate float64 `json:"profit_rate"` // 收益率 %
	Sold       bool    `json:"sold"`
}
