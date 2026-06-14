package main

import (
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/tdx/protocol"
)

// =========================================================
// 响应数据结构
// =========================================================

// BuyItem - 买入信号条目
type BuyItem struct {
	Code       string  `json:"code"`        // 股票代码（带前缀）
	Name       string  `json:"name"`        // 股票名称
	Date       string  `json:"date"`        // 日期 YYYY-MM-DD
	Time       string  `json:"time"`        // 信号产生时间
	Price      float64 `json:"price"`       // 买入价
	Rise       float64 `json:"rise"`        // 盘中涨幅百分比（仅当日买点有效）
	CurrPrice  float64 `json:"curr_price"`  // 现价
	IncomeRate float64 `json:"income_rate"` // 收益率百分比
	Sold       bool    `json:"sold"`        // 是否已卖出
	SellPrice  float64 `json:"sell_price"`  // 卖出价（已卖出时有效）
	SellTime   string  `json:"sell_time"`   // 卖出时间（已卖出时有效）
}

// BuyResponse - 买点响应
type BuyResponse struct {
	Type    string    `json:"type"`    // 固定 "buy"
	Count   int       `json:"count"`   // 数量
	Time    string    `json:"time"`    // 刷新时间
	Results []BuyItem `json:"results"` // 列表
}

// Trade - 卖出信号条目
type Trade struct {
	ID         int64   `json:"id"`                //唯一标识
	Code       string  `json:"code"`              // 股票代码
	Name       string  `json:"name"`              // 股票名称
	BuyTime    string  `json:"buy_time"`          // 买入时间
	BuyPrice   float64 `json:"buy_price"`         // 买入价
	Sold       bool    `json:"sold" xorm:"index"` //是否卖出
	SellTime   string  `json:"sell_time"`         // 卖出时间
	SellPrice  float64 `json:"sell_price"`        // 卖出价
	ProfitRate float64 `json:"profit_rate"`       // 收益率百分比
}

func (this *Trade) Sell(s *core.Sell) *Trade {
	this.Sold = true
	this.SellTime = s.Time.Format(time.DateTime)
	this.SellPrice = s.Price.Float64()
	if s.Price > 0 {
		this.ProfitRate = (this.SellPrice - this.BuyPrice) / this.SellPrice
	}
	return this
}

func (this *Trade) Buy() (core.Buy, error) {
	t, err := time.Parse(time.DateTime, this.BuyTime)
	return core.Buy{
		Code:  this.Code,
		Time:  t,
		Price: protocol.Yuan(this.BuyPrice),
	}, err
}

// SellResponse - 卖点响应
type SellResponse struct {
	Type    string  `json:"type"`    // 固定 "sell"
	Count   int     `json:"count"`   // 数量
	Time    string  `json:"time"`    // 刷新时间
	Results []Trade `json:"results"` // 列表
}

// HistoryResponse - 历史买点响应（扁平结构，按时间倒序）
type HistoryResponse struct {
	Type    string    `json:"type"`    // 固定 "history"
	Time    string    `json:"time"`    // 刷新时间
	Total   int       `json:"total"`   // 总买点数量
	Results []BuyItem `json:"results"` // 所有历史买点，按时间倒序
}
