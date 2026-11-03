package lab

import (
	"encoding/json"
	"fmt"
)

// Private snapshots written before tdx v2 contain milli prices; public API prices already use yuan.
func (m practiceMarket) MarshalJSON() ([]byte, error) {
	type plain practiceMarket
	m.PriceUnit = "yuan"
	return json.Marshal(plain(m))
}
func (m *practiceMarket) UnmarshalJSON(b []byte) error {
	type plain practiceMarket
	var v plain
	if e := json.Unmarshal(b, &v); e != nil {
		return e
	}
	if v.PriceUnit != "" && v.PriceUnit != "yuan" {
		return fmt.Errorf("未知行情价格单位 %q", v.PriceUnit)
	}
	if v.PriceUnit == "" {
		for i := range v.Days {
			d := &v.Days[i]
			d.Open /= 1000
			d.High /= 1000
			d.Low /= 1000
			d.Close /= 1000
		}
	}
	v.PriceUnit = "yuan"
	*m = practiceMarket(v)
	return nil
}
func (s practiceSession) MarshalJSON() ([]byte, error) {
	type plain practiceSession
	s.PriceUnit = "yuan"
	return json.Marshal(plain(s))
}
func (s *practiceSession) UnmarshalJSON(b []byte) error {
	type plain practiceSession
	var v plain
	if e := json.Unmarshal(b, &v); e != nil {
		return e
	}
	if v.PriceUnit != "" && v.PriceUnit != "yuan" {
		return fmt.Errorf("未知会话价格单位 %q", v.PriceUnit)
	}
	if v.PriceUnit == "" {
		v.Cost.Slippage /= 1000
	}
	v.PriceUnit = "yuan"
	*s = practiceSession(v)
	return nil
}
func (s practiceBank) MarshalJSON() ([]byte, error) {
	type plain practiceBank
	s.PriceUnit = "yuan"
	return json.Marshal(plain(s))
}
func (s *practiceBank) UnmarshalJSON(b []byte) error {
	type plain practiceBank
	var v plain
	if e := json.Unmarshal(b, &v); e != nil {
		return e
	}
	if v.PriceUnit != "" && v.PriceUnit != "yuan" {
		return fmt.Errorf("未知题库价格单位 %q", v.PriceUnit)
	}
	if v.PriceUnit == "" {
		v.Cost.Slippage /= 1000
	}
	v.PriceUnit = "yuan"
	*s = practiceBank(v)
	return nil
}
