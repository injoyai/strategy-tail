package lab

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/internal/marketstore"
	"github.com/injoyai/strategy-tail/internal/testmarket"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/protocol"
)

func TestConcentrationRanksAndSumsTurnover(t *testing.T) {
	rows := []marketstore.StockTurnover{
		{Code: "sz000001", Amount: 100},
		{Code: "sh600000", Amount: 25},
		{Code: "bj920001", Amount: 100},
		{Code: "sh600001", Amount: 0},
		{Code: "sh510300", Amount: 1000},
	}
	point := concentrationForDay(rows, 5)
	if point.Share == nil || *point.Share != 100.0/225 || point.Selected != 1 || point.Shanghai != 1 || point.Shenzhen != 1 || point.Beijing != 1 || point.Excluded != 1 {
		t.Fatalf("unexpected concentration point: %+v", point)
	}
	topHalf := concentrationForDay(rows, 50)
	if topHalf.Share == nil || *topHalf.Share != 200.0/225 || topHalf.Selected != 2 {
		t.Fatalf("unexpected turnover share: %+v", topHalf)
	}
	amount := concentrationForDay(rows, 100)
	if amount.Share == nil || *amount.Share != 1 || amount.Selected != 3 {
		t.Fatalf("unexpected amount share: %+v", amount)
	}
	empty := concentrationForDay(rows[3:4], 5)
	if empty.Share != nil || empty.Selected != 0 || empty.Excluded != 1 {
		t.Fatalf("unavailable share must be null: %+v", empty)
	}
}

func TestIndexResearchRejectsInvalidParameters(t *testing.T) {
	root := t.TempDir()
	s := newServerWithStores(NewAnalysisStore(filepath.Join(root, "analysis")), NewCandidateStore(filepath.Join(root, "candidates")))
	base := "/api/index-research/series?index=sh000001&from=2024-01-01&to=2024-01-31&topPercent="
	for _, request := range []string{base + "0", base + "101", base + "5.5", base + "5&rankBy=volume", base + "5&shareOf=volume", "/api/index-research/series?index=sh600000", "/api/index-research/series?index=sh000001"} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, request, nil))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, body %s", request, w.Code, w.Body.String())
		}
	}
}

type indexResearchFixture struct {
	*testmarket.Store
	turnover []marketstore.StockTurnover
}

func (f indexResearchFixture) ReadStockTurnover(_ context.Context, from, to string, visit func(marketstore.StockTurnover)) error {
	for _, row := range f.turnover {
		if row.Date >= from && row.Date <= to {
			visit(row)
		}
	}
	return nil
}

func TestIndexResearchAlignsDatesWithoutFillingGaps(t *testing.T) {
	fixture := indexResearchFixture{Store: testmarket.New(), turnover: []marketstore.StockTurnover{
		{Date: "2024-01-03", Code: "sh600000", Amount: 100},
		{Date: "2024-01-03", Code: "sz000002", Amount: 300},
		{Date: "2024-01-04", Code: "bj920001", Amount: 400},
	}}
	fixture.SeedDay(model.Code{Market: protocol.MarketIndex, Code: "sh000001"}, []*model.KlineDay{
		{Time: "2023-12-29", Close: 2800}, {Time: "2024-01-02", Close: 3000}, {Time: "2024-01-03", Close: 3100},
	})
	before := common.Pull
	common.Pull = extend.NewMarketReader(fixture, nil)
	t.Cleanup(func() { common.Pull = before })
	root := t.TempDir()
	s := newServerWithStores(NewAnalysisStore(filepath.Join(root, "analysis")), NewCandidateStore(filepath.Join(root, "candidates")))

	catalog := httptest.NewRecorder()
	s.Handler().ServeHTTP(catalog, httptest.NewRequest(http.MethodGet, "/api/index-research/indexes", nil))
	var indexes []researchIndex
	if err := json.Unmarshal(catalog.Body.Bytes(), &indexes); err != nil || len(indexes) != 1 || indexes[0].Name != "上证指数" {
		t.Fatalf("unexpected index catalog: %s", catalog.Body.String())
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/index-research/series?index=sh000001&from=2024-01-02&to=2024-01-04&topPercent=5", nil))
	var points []indexResearchPoint
	if err := json.Unmarshal(w.Body.Bytes(), &points); err != nil || len(points) != 3 {
		t.Fatalf("unexpected response: %s", w.Body.String())
	}
	if points[0].Date != "2024-01-02" || points[0].Concentration != nil || points[0].Close == nil || *points[0].Close != 3000 ||
		points[1].Date != "2024-01-03" || points[1].Close == nil || *points[1].Close != 3100 || points[1].Concentration == nil ||
		points[1].Concentration.Share == nil || *points[1].Concentration.Share != 0.75 || points[2].Date != "2024-01-04" || points[2].Close != nil {
		t.Fatalf("date alignment lost a point or filled a gap: %s", w.Body.String())
	}
}
