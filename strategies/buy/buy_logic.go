package buy

import (
	"strings"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

type And []core.Buyer

func (a And) Name() string {
	if len(a) == 0 {
		return "Null"
	}
	if len(a) == 1 {
		return a[0].Name()
	}
	names := make([]string, 0, len(a))
	for _, v := range a {
		if v == nil {
			continue
		}
		names = append(names, v.Name())
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

type Or []core.Buyer

func (s Or) Name() string {
	if len(s) == 0 {
		return "Null"
	}
	if len(s) == 1 {
		return s[0].Name()
	}
	names := make([]string, 0, len(s))
	for _, v := range s {
		if v == nil {
			continue
		}
		names = append(names, v.Name())
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

func Not(b core.Buyer) core.Buyer {
	return not{b}
}

type not struct {
	core.Buyer
}

func (n not) Name() string {
	return "过滤" + n.Buyer.Name()
}

func (n not) Buy(code string, dks extend.Klines) bool {
	return !n.Buyer.Buy(code, dks)
}

type A全部 struct{}

func (n A全部) Name() string {
	return "全部"
}

func (n A全部) Buy(code string, dks extend.Klines) bool {
	return true
}
