package extend

import "github.com/injoyai/tdx/v2/extend/metadata"

// MarketData contains the code and equity snapshots read from MySQL.
type MarketData struct {
	Codes  *metadata.Codes
	Equity *metadata.Gbbq
}
