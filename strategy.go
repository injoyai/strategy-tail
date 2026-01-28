package main

import (
	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

type Strategy interface {
	// Signal 传入 日线[上市,日期A] 分钟线[日期A]
	Signal(dks extend.Klines, mk protocol.Klines) bool
}

// 策略1
type s1 struct{}

func (s1) Signal(dks extend.Klines, mks protocol.Klines) bool {
	if len(dks) == 0 || len(mks) == 0 {
		return false
	}

	dk := dks[len(dks)-1]

	if dk.Turnover > 5 || dk.Turnover < 2 {
		return false
	}

	return true
}
