package main

import (
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/injoyai/strategy-tail/model"
	"github.com/injoyai/strategy-tail/service"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func main() {
	r := gin.Default()

	// Initialize Service
	dataService := service.NewDataService()
	go dataService.StartMarketUpdate() // Start simulating market updates

	// API Routes
	api := r.Group("/api")
	{
		api.GET("/stocks", func(c *gin.Context) {
			// Get filter params
			minMarketCap, _ := strconv.ParseFloat(c.Query("min_market_cap"), 64)
			maxMarketCap, _ := strconv.ParseFloat(c.Query("max_market_cap"), 64)

			filter := model.StockFilter{
				MinMarketCap: minMarketCap,
				MaxMarketCap: maxMarketCap,
				// Add more filters as needed
			}

			stocks := dataService.GetStocks(filter)
			c.JSON(http.StatusOK, stocks)
		})

		api.POST("/backtest", func(c *gin.Context) {
			var params model.BacktestParams
			if err := c.ShouldBindJSON(&params); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			result := service.RunBacktest(dataService, params)
			c.JSON(http.StatusOK, result)
		})
	}

	// WebSocket for real-time updates
	r.GET("/ws", func(c *gin.Context) {
		wsHandler(c.Writer, c.Request, dataService)
	})

	log.Println("Server starting on :8080")
	r.Run(":8080")
}

func wsHandler(w http.ResponseWriter, r *http.Request, ds *service.DataService) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Upgrade error:", err)
		return
	}
	defer conn.Close()

	// Subscribe to updates
	updateChan := ds.Subscribe()
	defer ds.Unsubscribe(updateChan)

	for {
		select {
		case stocks := <-updateChan:
			// Send updated stock data (simplified for bandwidth)
			if err := conn.WriteJSON(stocks); err != nil {
				log.Println("Write error:", err)
				return
			}
		}
	}
}
