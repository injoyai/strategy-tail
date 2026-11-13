package main

import (
	"github.com/injoyai/strategy-tail/internal/screener"
	"github.com/injoyai/strategy-tail/lib/extend"
)

func screenStrategies(ks extend.Klines) []Strategy {
	presets := screener.Presets(ks)
	strategies := make([]Strategy, 0, len(presets))
	for _, preset := range presets {
		strategies = append(strategies, Strategy(preset))
	}
	return strategies
}
