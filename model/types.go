package model

// Stock represents a single stock's metadata and current status
type Stock struct {
	Code      string  `json:"code"`
	Name      string  `json:"name"`
	Price     float64 `json:"price"`
	Change    float64 `json:"change"`     // Percentage
	MarketCap float64 `json:"market_cap"` // In Billions
	KLines    []KLine `json:"k_lines"`    // Recent KLines for display
}

// KLine represents a single candle
type KLine struct {
	Date   string  `json:"date"`
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume float64 `json:"volume"`
	MA5    float64 `json:"ma5"`
	MA10   float64 `json:"ma10"`
	MA20   float64 `json:"ma20"`
}

type StockFilter struct {
	MinMarketCap float64
	MaxMarketCap float64
}

type BacktestParams struct {
	StrategyType string  `json:"strategy_type"`
	StartDate    string  `json:"start_date"`
	EndDate      string  `json:"end_date"`
	InitialCash  float64 `json:"initial_cash"`
	// Add strategy specific params
	Threshold float64 `json:"threshold"`
}

type BacktestResult struct {
	TotalReturn float64       `json:"total_return"`
	WinRate     float64       `json:"win_rate"`
	Trades      []Trade       `json:"trades"`
	EquityCurve []EquityPoint `json:"equity_curve"`
}

type Trade struct {
	StockCode string  `json:"stock_code"`
	BuyDate   string  `json:"buy_date"`
	SellDate  string  `json:"sell_date"`
	BuyPrice  float64 `json:"buy_price"`
	SellPrice float64 `json:"sell_price"`
	Profit    float64 `json:"profit"`
}

type EquityPoint struct {
	Date  string  `json:"date"`
	Value float64 `json:"value"`
}
