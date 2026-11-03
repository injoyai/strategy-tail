package extend

import (
	"github.com/injoyai/tdx/v2"
	"github.com/injoyai/tdx/v2/protocol"
)

// FetchStandardDay adapts the v2 source-date response for the existing Chinese-market backtest model.
func FetchStandardDay(c tdx.Client, code string, stop func(*protocol.Kline) bool) (protocol.Klines, error) {
	r, e := c.GetKlineDayUntil(code, func(k tdx.Kline) bool {
		return k.Standard != nil && stop(k.Standard)
	})
	if e != nil || r == nil {
		return nil, e
	}
	return r.Klines()
}

// FetchStandardMinute keeps standard protocol volume in integer lots for existing strategy consumers.
func FetchStandardMinute(c tdx.Client, code string, stop func(*protocol.Kline) bool, splitAuction bool) (protocol.Klines, error) {
	f := c.GetKlineMinuteUntil
	if splitAuction && !protocol.IsIndex(code) {
		f = c.GetKlineMinute241Until
	}
	r, e := f(code, func(k tdx.Kline) bool {
		return k.Standard != nil && stop(k.Standard)
	})
	if e != nil || r == nil {
		return nil, e
	}
	return r.Klines()
}
