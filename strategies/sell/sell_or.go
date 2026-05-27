package sell

import (
	"strings"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/tdx/extend"
)

type Or []core.Seller

func (s Or) Name() string {
	names := make([]string, 0, len(s))
	for _, v := range s {
		if v == nil {
			continue
		}
		names = append(names, v.Name())
	}
	if len(names) == 0 {
		return "Null"
	}
	return "[" + strings.Join(names, " | ") + "]"
}

func (s Or) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	for _, v := range s {
		if v == nil {
			continue
		}
		if v.Sell(code, dks, buy) {
			return true
		}
	}
	return false
}
