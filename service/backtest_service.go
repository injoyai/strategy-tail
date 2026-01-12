package service

import (
	"math/rand"
	"time"

	"github.com/injoyai/strategy-tail/model"
)

// RunBacktest is a simplified backtest engine
func RunBacktest(ds *DataService, params model.BacktestParams) model.BacktestResult {
	// In a real system, we would load historical data for the period.
	// Here we will simulate a backtest result for demonstration.

	rand.Seed(time.Now().UnixNano())

	initialCapital := params.InitialCash
	currentCapital := initialCapital

	trades := []model.Trade{}
	equityCurve := []model.EquityPoint{}

	// Simulate 20 trades
	for i := 0; i < 20; i++ {
		isWin := rand.Float64() > 0.4 // 60% win rate
		var profit float64
		if isWin {
			profit = currentCapital * 0.05 // 5% profit
		} else {
			profit = -currentCapital * 0.02 // 2% loss
		}

		currentCapital += profit

		trades = append(trades, model.Trade{
			StockCode: "600000",
			BuyDate:   "2023-01-01",
			SellDate:  "2023-01-05",
			BuyPrice:  10.0,
			SellPrice: 10.0 + (profit/currentCapital)*10,
			Profit:    profit,
		})

		equityCurve = append(equityCurve, model.EquityPoint{
			Date:  time.Now().AddDate(0, 0, -20+i).Format("2006-01-02"),
			Value: currentCapital,
		})
	}

	totalReturn := (currentCapital - initialCapital) / initialCapital * 100

	return model.BacktestResult{
		TotalReturn: totalReturn,
		WinRate:     0.60,
		Trades:      trades,
		EquityCurve: equityCurve,
	}
}
