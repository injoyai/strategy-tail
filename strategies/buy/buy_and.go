package buy

import (
	"strings"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/tdx/extend"
)

type And []core.Buyer

func (a And) Name() string {
	names := make([]string, 0, len(a))
	for _, v := range a {
		if v == nil {
			continue
		}
		names = append(names, v.Name())
	}
	if len(names) == 0 {
		return "Null"
	}
	return "[" + strings.Join(names, " & ") + "]"
}

func (a And) Buy(code string, dks extend.Klines) bool {
	buy := false
	for _, v := range a {
		if v == nil {
			continue
		}
		buy = true
		if !v.Buy(code, dks) {
			return false
		}
	}
	return buy
}
