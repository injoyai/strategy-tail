package lab

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/internal/testmarket"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/researchdata"
	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/protocol"
)

func TestFactorValidationHTTPFreezeRunAndCreateFirstModel(t *testing.T) {
	t.Chdir(t.TempDir())
	market := testmarket.New()
	oldPull := common.Pull
	common.Pull = extend.NewMarketReader(market, nil)
	t.Cleanup(func() { common.Pull = oldPull })
	first := time.Date(2024, 12, 23, 0, 0, 0, 0, protocol.Location())
	dates := []string{}
	for i := range 65 {
		d := first.AddDate(0, 0, i)
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			dates = append(dates, d.Format(time.DateOnly))
		}
	}
	bench := make([]*model.KlineDay, len(dates))
	for i, day := range dates {
		bench[i] = &model.KlineDay{Time: day, Open: 100, Close: 100, Volume: 1000}
	}
	market.SeedDay(model.Code{Market: protocol.MarketIndex, Code: factorCalendarCode}, bench)
	members := []researchdata.MembershipRecord{}
	for i := range 12 {
		code := fmt.Sprintf("sh%06d", 600000+i)
		members = append(members, researchdata.MembershipRecord{Code: code, ListedAt: first, AvailableAt: first})
		rows := make([]*model.KlineDay, len(dates))
		for j, day := range dates {
			price := 10 + float64(i+1)*float64(j)*0.05
			rows[j] = &model.KlineDay{Time: day, Open: price, High: price, Low: price, Close: price, Volume: 10000}
		}
		market.SeedDay(model.Code{Market: protocol.MarketAStock, Code: code}, rows)
	}
	universe, err := researchdata.NewMembershipUniverse(members)
	if err != nil {
		t.Fatal(err)
	}
	analyses := NewAnalysisStore(t.TempDir())
	s := newServerWithStores(analyses, newTestCandidateStore(t))
	s.runner.universe = universe
	s.runner.trials = NewTrialStore(t.TempDir())
	rep := v4ReportForTest(t, testAnalysisID, "momentum", "2025-01-03T15:00:00+08:00")
	rep.FirstDataDate, rep.LastDataDate = dates[0], "2024-12-31"
	rep.Range.StartYear, rep.Range.EndYear = 2024, 2024
	p := rep.Protocol
	p.Labels = LabelSpec{Kind: labelKindNextOpenHeldSessions, Horizons: []int{1}}
	rep.Horizons = p.Labels.Horizons
	rep.Window = 1
	p.Universe.ID, p.Universe.Version = universe.Snapshot().ID, universe.Snapshot().Version
	rep.ProtocolHash, err = protocolHash(*p)
	if err != nil {
		t.Fatal(err)
	}
	if err := analyses.Save(rep); err != nil {
		t.Fatal(err)
	}
	candidate, _, err := s.candidateStore.Create(observeReq(testUUID1, "动量"), rep)
	if err != nil {
		t.Fatal(err)
	}
	trial, err := s.runner.trials.Start(*p, rep.Factor, rep.AnalysisID)
	if err != nil {
		t.Fatal(err)
	}
	doReq(t, s.Handler(), http.MethodGet, "/api/factor-candidates/"+candidate.ID+"/validation-inputs", nil, http.StatusOK)
	req := validCreateReq(validationStoreFixture{Candidate: candidate, Trial: trial}, testUUID2)
	req.Direction = labelDirectionPositive
	req.TestRange = &DateRange{Start: "2024-12-31", End: "2025-02-14"}
	req.Policy.MinValidWindows = 1
	doReq(t, s.Handler(), http.MethodPost, "/api/factor-validations", req, http.StatusBadRequest)
	req.TestRange.Start = "2025-01-01"
	raw := doReq(t, s.Handler(), http.MethodPost, "/api/factor-validations", req, http.StatusCreated)
	var frozen struct{ Validation ValidationRequest }
	if err := json.Unmarshal(raw, &frozen); err != nil {
		t.Fatal(err)
	}
	id := frozen.Validation.ID
	doReq(t, s.Handler(), http.MethodPost, "/api/factor-models", createModelBody(id, testUUID1), http.StatusBadRequest)
	doReq(t, s.Handler(), http.MethodPost, "/api/factor-validations/"+id+"/run", nil, http.StatusAccepted)
	deadline := time.Now().Add(5 * time.Second)
	for s.runner.running.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if s.runner.running.Load() || s.runner.Status()["state"] != "done" || s.runner.Status()["runId"] != id {
		t.Fatalf("validation job did not complete: %v", s.runner.Status())
	}
	raw = doReq(t, s.Handler(), http.MethodGet, "/api/factor-validations/"+id, nil, http.StatusOK)
	var view ValidationView
	if err := json.Unmarshal(raw, &view); err != nil || view.State != ValidationStatePassed {
		t.Fatalf("validation result %s, %v", raw, err)
	}
	w := view.Windows[0]
	if w.Observations != 29 || w.Horizons[0].TopExcess == nil || *w.Horizons[0].TopExcess <= 0 || w.Horizons[0].Coverage != 1 {
		t.Fatalf("test boundary, coverage or benchmark: %+v / %+v", w, w.Horizons[0])
	}
	doReq(t, s.Handler(), http.MethodGet, "/api/factor-validations?state=passed", nil, http.StatusOK)
	got := createModelViaAPI(t, s.Handler(), id, testUUID1)
	if got.ValidatedFactors[0].ValidationID != id {
		t.Fatalf("model did not consume the new validation: %+v", got)
	}
}
