package common

import "github.com/injoyai/strategy-tail/lib/extend"

// OpenResearchMarket 按项目的 pull.mysql 配置打开只读行情接口，由调用方关闭。
func OpenResearchMarket() (*extend.MarketReader, error) {
	dsn, err := MySQLDSN()
	if err != nil {
		return nil, err
	}
	return extend.OpenMarketReader(dsn)
}
