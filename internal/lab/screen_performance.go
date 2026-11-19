package lab

import (
	"context"
	"fmt"
	"log"
	"slices"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/screener"
	"github.com/injoyai/tdx/v2/protocol"
)

type screenPerformance struct {
	TradeStatsJSON
	Closed   int `json:"closed"`
	Marked   int `json:"marked"`
	Unpriced int `json:"unpriced"`
}

func summarizeScreenPerformance(rows []screener.Signal) screenPerformance {
	var out screenPerformance
	trades := make([]core.Trade, 0, len(rows))
	for _, row := range rows {
		price := row.SellPrice
		switch {
		case row.SellTime != "":
			out.Closed++
		case validScreenPrice(row.LastPrice):
			price = row.LastPrice
			out.Marked++
		default:
			out.Unpriced++
			continue
		}
		trades = append(trades, core.Trade{BuyPrice: row.BuyPrice, SellPrice: price})
	}
	out.TradeStatsJSON = newTradeStatsJSON(core.Stats(trades))
	return out
}

type screenPriceMarket interface {
	stockLatestDayCloses(context.Context, []string, string) (map[string]*protocol.Kline, error)
}

// markScreenPositions values open positions for display without updating the ledger.
func (s *screenService) markScreenPositions(ctx context.Context, rows []screener.Signal, at time.Time) string {
	var codes []string
	for _, row := range rows {
		if row.SellTime == "" {
			codes = append(codes, row.Code)
		}
	}
	if len(codes) == 0 {
		return ""
	}
	slices.Sort(codes)
	codes = slices.Compact(codes)
	at = at.In(protocol.Location())
	cutoff := at
	if at.Hour() < 15 {
		cutoff = at.AddDate(0, 0, -1)
	}
	daily, err := s.latestScreenDayCloses(ctx, codes, cutoff)
	if err != nil {
		log.Printf("[screen] 读取持仓日线收盘价失败: %v", err)
	}
	minutes, minuteErr := s.market.StockSessionBars(codes, at)
	if minuteErr != nil {
		log.Printf("[screen] 读取持仓盘中价格失败: %v", minuteErr)
	}
	for i := range rows {
		row := &rows[i]
		if row.SellTime != "" {
			continue
		}
		row.LastPrice, row.Rate = 0, 0
		price := daily[row.Code]
		if minute := minutes[row.Code]; usableScreenMark(minute, at, row.BuyTime) &&
			(!usableScreenMark(price, at, row.BuyTime) || minute.Time.After(price.Time)) {
			price = minute
		}
		if usableScreenMark(price, at, row.BuyTime) {
			row.LastPrice = price.Close
		}
		row.SetRate()
	}
	if err != nil || minuteErr != nil {
		return "部分最新行情读取失败。"
	}
	return ""
}

func usableScreenMark(bar *protocol.Kline, at time.Time, buyTime string) bool {
	return bar != nil && validScreenPrice(bar.Close) && !bar.Time.After(at) &&
		bar.Time.In(protocol.Location()).Format(time.DateTime) >= buyTime
}

func (s *screenService) latestScreenDayCloses(ctx context.Context, codes []string, cutoff time.Time) (map[string]*protocol.Kline, error) {
	if market, ok := s.market.(screenPriceMarket); ok {
		return market.stockLatestDayCloses(ctx, codes, cutoff.Format(time.DateOnly))
	}
	out := make(map[string]*protocol.Kline, len(codes))
	var first error
	for _, code := range codes {
		bars, err := s.market.DayKlines(code, time.Time{}, cutoff)
		if err != nil && first == nil {
			first = fmt.Errorf("%s: %w", code, err)
		}
		for i := len(bars) - 1; i >= 0; i-- {
			if usableScreenMark(bars[i].Kline, core.DayOf(cutoff).Add(15*time.Hour), "") {
				out[code] = bars[i].Kline
				break
			}
		}
	}
	return out, first
}
