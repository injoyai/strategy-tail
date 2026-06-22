package buy

import (
	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

// A创业板 是只买入创业板股票的过滤条件。
// 创业板代码前缀为 sz30。
type A创业板 struct{}

func (b A创业板) Name() string {
	return "创业板"
}

func (b A创业板) Buy(code string, dks extend.Klines) bool {
	code = protocol.AddPrefix(code)
	return len(code) == 8 && code[:4] == "sz30"
}

// A科创板 是只买入科创板股票的过滤条件。
// 科创板代码前缀为 sh68。
type A科创板 struct{}

func (b A科创板) Name() string {
	return "科创板"
}

func (b A科创板) Buy(code string, dks extend.Klines) bool {
	code = protocol.AddPrefix(code)
	return len(code) == 8 && code[:4] == "sh68"
}

// A北证板 是只买入北交所股票的过滤条件。
// 北交所代码前缀为 bj。
type A北证板 struct{}

func (b A北证板) Name() string {
	return "北证板"
}

func (b A北证板) Buy(code string, dks extend.Klines) bool {
	code = protocol.AddPrefix(code)
	return len(code) == 8 && code[:2] == "bj"
}
