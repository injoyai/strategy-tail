package extend

import "math"

// RisePercent returns the strategy-layer percentage (1 means 1%).
func (k *Kline) RisePercent() (float64, bool) {
	if k == nil {
		return 0, false
	}
	rate, ok := k.Kline.RiseRate()
	percent := rate * 100
	if !ok || math.IsInf(percent, 0) {
		return 0, false
	}
	return percent, true
}
