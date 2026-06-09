package buy

import (
	"fmt"

	"github.com/injoyai/tdx/extend"
)

type A流通市值 struct {
	Min float64 //亿元
	Max float64 //亿元
}

func (b A流通市值) Name() string {
	switch {
	case b.Min > 0 && b.Max > 0:
		return fmt.Sprintf("流通市值[%.f,%.f]亿", b.Min, b.Max)
	case b.Min > 0:
		return fmt.Sprintf("流通市值[%.f,]亿", b.Min)
	case b.Max > 0:
		return fmt.Sprintf("流通市值[,%.f]亿", b.Max)
	default:
		return "Null"
	}
}

func (b A流通市值) Buy(code string, dks extend.Klines) bool {
	if len(dks) == 0 {
		return false
	}
	last := dks[len(dks)-1]
	if b.Min > 0 && last.FloatValue().Float64()/1e8 < b.Min {
		return false
	}
	if b.Max > 0 && last.FloatValue().Float64()/1e8 > b.Max {
		return false
	}
	return true
}
