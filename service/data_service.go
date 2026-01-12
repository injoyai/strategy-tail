package service

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/injoyai/strategy-tail/model"
)

type DataService struct {
	stocks      map[string]*model.Stock
	subscribers []chan []model.Stock
	mu          sync.RWMutex
}

func NewDataService() *DataService {
	ds := &DataService{
		stocks: make(map[string]*model.Stock),
	}
	ds.initMockData()
	return ds
}

func (ds *DataService) initMockData() {
	// Create 100 mock stocks
	for i := 0; i < 100; i++ {
		code := fmt.Sprintf("600%03d", i)
		name := fmt.Sprintf("Stock-%d", i)
		basePrice := 10.0 + rand.Float64()*90.0

		kLines := generateMockKLines(basePrice, 30) // Generate 30 days of history
		currentPrice := kLines[len(kLines)-1].Close

		ds.stocks[code] = &model.Stock{
			Code:      code,
			Name:      name,
			Price:     currentPrice,
			Change:    (currentPrice - kLines[len(kLines)-2].Close) / kLines[len(kLines)-2].Close * 100,
			MarketCap: 10 + rand.Float64()*100, // 10B - 110B
			KLines:    kLines,
		}
	}
}

func generateMockKLines(basePrice float64, days int) []model.KLine {
	var klines []model.KLine
	price := basePrice
	now := time.Now().AddDate(0, 0, -days)

	for i := 0; i < days; i++ {
		change := (rand.Float64() - 0.5) * 0.1 // +/- 5%
		open := price * (1 + (rand.Float64()-0.5)*0.02)
		closePrice := price * (1 + change)
		high := max(open, closePrice) * (1 + rand.Float64()*0.02)
		low := min(open, closePrice) * (1 - rand.Float64()*0.02)

		klines = append(klines, model.KLine{
			Date:   now.AddDate(0, 0, i).Format("2006-01-02"),
			Open:   open,
			High:   high,
			Low:    low,
			Close:  closePrice,
			Volume: 100000 + rand.Float64()*1000000,
		})
		price = closePrice
	}

	// Calculate MA
	for i := range klines {
		klines[i].MA5 = calculateMA(klines, i, 5)
		klines[i].MA10 = calculateMA(klines, i, 10)
		klines[i].MA20 = calculateMA(klines, i, 20)
	}

	return klines
}

func calculateMA(klines []model.KLine, index int, period int) float64 {
	if index < period-1 {
		return 0
	}
	sum := 0.0
	for i := 0; i < period; i++ {
		sum += klines[index-i].Close
	}
	return sum / float64(period)
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func (ds *DataService) GetStocks(filter model.StockFilter) []model.Stock {
	ds.mu.RLock()
	defer ds.mu.RUnlock()

	var result []model.Stock
	for _, stock := range ds.stocks {
		// Apply filters
		if filter.MinMarketCap > 0 && stock.MarketCap < filter.MinMarketCap {
			continue
		}
		if filter.MaxMarketCap > 0 && stock.MarketCap > filter.MaxMarketCap {
			continue
		}
		result = append(result, *stock)
	}
	return result
}

func (ds *DataService) StartMarketUpdate() {
	ticker := time.NewTicker(2 * time.Second)
	for range ticker.C {
		ds.updatePrices()
		ds.broadcast()
	}
}

func (ds *DataService) updatePrices() {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	for _, stock := range ds.stocks {
		change := (rand.Float64() - 0.5) * 0.01 // +/- 0.5% fluctuation
		stock.Price = stock.Price * (1 + change)

		// Update last KLine close price for real-time feel (simplified)
		lastIdx := len(stock.KLines) - 1
		stock.KLines[lastIdx].Close = stock.Price
		if stock.Price > stock.KLines[lastIdx].High {
			stock.KLines[lastIdx].High = stock.Price
		}
		if stock.Price < stock.KLines[lastIdx].Low {
			stock.KLines[lastIdx].Low = stock.Price
		}
	}
}

func (ds *DataService) Subscribe() chan []model.Stock {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	ch := make(chan []model.Stock, 1)
	ds.subscribers = append(ds.subscribers, ch)
	return ch
}

func (ds *DataService) Unsubscribe(ch chan []model.Stock) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	for i, sub := range ds.subscribers {
		if sub == ch {
			ds.subscribers = append(ds.subscribers[:i], ds.subscribers[i+1:]...)
			close(ch)
			break
		}
	}
}

func (ds *DataService) broadcast() {
	ds.mu.RLock()
	stocks := make([]model.Stock, 0, len(ds.stocks))
	for _, s := range ds.stocks {
		stocks = append(stocks, *s)
	}
	ds.mu.RUnlock()

	ds.mu.Lock()
	defer ds.mu.Unlock()
	for _, ch := range ds.subscribers {
		select {
		case ch <- stocks:
		default:
		}
	}
}
