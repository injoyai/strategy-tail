package extend

import (
	"testing"
	"time"

	"github.com/injoyai/tdx/v2"
	"github.com/injoyai/tdx/v2/protocol"
)

type standardProbe struct {
	tdx.Client
	response *tdx.KlineResp
	split    bool
}

func (p *standardProbe) GetKlineDayUntil(_ string, stop func(tdx.Kline) bool) (*tdx.KlineResp, error) {
	if !stop(p.response.List[0]) {
		panic("full standard bar was not forwarded")
	}
	return p.response, nil
}
func (p *standardProbe) GetKlineMinuteUntil(_ string, stop func(tdx.Kline) bool) (*tdx.KlineResp, error) {
	p.split = false
	return p.GetKlineDayUntil("", stop)
}
func (p *standardProbe) GetKlineMinute241Until(_ string, stop func(tdx.Kline) bool) (*tdx.KlineResp, error) {
	p.split = true
	return p.GetKlineDayUntil("", stop)
}

func TestV2StandardResponseKeepsPriceTimeAndLotVolume(t *testing.T) {
	at := time.Date(2026, 1, 2, 15, 0, 0, 0, protocol.Location())
	raw := &protocol.Kline{Time: at, Close: 12.34567, Volume: 1000}
	p := &standardProbe{response: &tdx.KlineResp{Instrument: protocol.Instrument{Exchange: protocol.ExchangeSH, Code: "000001"}, List: []tdx.Kline{{Time: "2026-01-02 15:00", Close: raw.Close, Volume: 1000, Standard: raw}}}}
	stop := func(k *protocol.Kline) bool { return k.Time.Equal(at) && k.Close == raw.Close }
	r, e := FetchStandardMinute(p, "sh000001", stop, true)
	if e != nil || len(r) != 1 || r[0].Volume != 10 || r[0].Close != raw.Close || p.split || raw.Volume != 1000 {
		t.Fatal(r, e, p.split)
	}
	if _, e = FetchStandardDay(p, "sh000001", stop); e != nil {
		t.Fatal(e)
	}
	p.response.Instrument.Code = "600000"
	if _, e = FetchStandardMinute(p, "sh600000", stop, true); e != nil || !p.split {
		t.Fatal(e, p.split)
	}
}

func TestV2StandardLongHistory(t *testing.T) {
	raw := &protocol.Kline{Time: time.Date(2026, 1, 2, 15, 0, 0, 0, protocol.Location()), Close: 10}
	p := &standardProbe{response: &tdx.KlineResp{List: make([]tdx.Kline, 65536)}}
	for i := range p.response.List {
		p.response.List[i] = tdx.Kline{Standard: raw}
	}
	got, e := FetchStandardMinute(p, "sh600000", func(*protocol.Kline) bool { return true }, false)
	if e != nil || len(got) != 65536 {
		t.Fatal(len(got), e)
	}
}
