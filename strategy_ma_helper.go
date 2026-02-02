package main

import "github.com/injoyai/tdx/extend"

// 辅助函数：计算最低点
func LowestLow(dks extend.Klines) float64 {
	if len(dks) == 0 {
		return 0
	}
	min := dks[0].Low.Float64()
	for _, k := range dks {
		if k.Low.Float64() < min {
			min = k.Low.Float64()
		}
	}
	return min
}
