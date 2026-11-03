package lab

import (
	"encoding/json"
	"testing"
)

func TestV2PracticeSnapshotPriceUnits(t *testing.T) {
	var old practiceMarket
	if e := json.Unmarshal([]byte(`{"version":1,"days":[{"open":12345,"high":13000,"low":12000,"close":12500,"ma5":12.4}]}`), &old); e != nil {
		t.Fatal(e)
	}
	if old.Days[0].Open != 12.345 || old.Days[0].Close != 12.5 || *old.Days[0].MA5 != 12.4 {
		t.Fatal("legacy units not converted independently of indicators", old)
	}
	raw, e := json.Marshal(old)
	if e != nil {
		t.Fatal(e)
	}
	var round practiceMarket
	if e = json.Unmarshal(raw, &round); e != nil {
		t.Fatal(e)
	}
	if round.PriceUnit != "yuan" || round.Days[0].Open != 12.345 {
		t.Fatal("converted twice", round)
	}
	if e = json.Unmarshal([]byte(`{"priceUnit":"unknown"}`), &round); e == nil {
		t.Fatal("unknown price unit accepted")
	}
	var session practiceSession
	if e = json.Unmarshal([]byte(`{"cost":{"Slippage":10}}`), &session); e != nil || session.Cost.Slippage != .01 {
		t.Fatal("legacy fee unit", session.Cost, e)
	}
}
