package main

import (
	"github.com/injoyai/strategy-tail/core"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
)

func Strategy() []core.Variant {
	return []core.Variant{
		{Name: "解释·收回MA5", Buyer: sb.And{
			sb.A阴线收回{SupportPeriod: 5, MinBodyRatio: 0.3, MaxRise: 1.0, Trend: sb.MAUp{Period: 5}},
		}},
		{Name: "解释·收回MA10", Buyer: sb.And{
			sb.A阴线收回{SupportPeriod: 10, MinBodyRatio: 0.3, MaxRise: 1.0, Trend: sb.MAUp{Period: 5}},
		}},
	}
}
