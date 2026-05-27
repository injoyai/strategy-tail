package buy

import (
	"strings"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/tdx/extend"
)

type Or []core.Buyer

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

func (s Or) Buy(code string, dks extend.Klines) bool {
	for _, v := range s {
		if v == nil {
			continue
		}
		if v.Buy(code, dks) {
			return true
		}
	}
	return false
}
