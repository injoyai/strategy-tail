package main

import (
	"log"

	"github.com/injoyai/frame/fbr"
	"github.com/injoyai/strategy-tail/model"
	"github.com/injoyai/strategy-tail/service"
)

func main() {
	r := fbr.Default()

	// Initialize Service
	dataService := service.NewDataService()
	go dataService.StartMarketUpdate() // Start simulating market updates

	// API Routes
	r.Group("/api", func(g fbr.Grouper) {
		g.GET("/stocks", func(c fbr.Ctx) {
			// Get filter params
			minMarketCap := c.GetFloat64("min_market_cap")
			maxMarketCap := c.GetFloat64("max_market_cap")

			filter := model.StockFilter{
				MinMarketCap: minMarketCap,
				MaxMarketCap: maxMarketCap,
				// Add more filters as needed
			}

			stocks := dataService.GetStocks(filter)
			c.Succ(stocks)
		})

		g.POST("/backtest", func(c fbr.Ctx) {
			var params model.BacktestParams
			c.Parse(&params)
			result := service.RunBacktest(dataService, params)
			c.Succ(result)
		})

	})

	// WebSocket for real-time updates
	r.GET("/ws", func(c fbr.Ctx) {
		wsHandler(c, dataService)
	})

	r.Run()
}

func wsHandler(c fbr.Ctx, ds *service.DataService) {
	c.Websocket(func(ws *fbr.Websocket) {
		// Subscribe to updates
		updateChan := ds.Subscribe()
		defer ds.Unsubscribe(updateChan)

		for {
			select {
			case stocks := <-updateChan:
				// Send updated stock data (simplified for bandwidth)
				if err := ws.WriteJSON(stocks); err != nil {
					log.Println("Write error:", err)
					return
				}
			}
		}
	})
}
