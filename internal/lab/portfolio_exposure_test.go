package lab

import (
	"math"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/internal/portfolioresearch"
	"github.com/injoyai/strategy-tail/researchdata"
	"github.com/injoyai/tdx/v2/protocol"
)

func TestPortfolioExposureUsesSignalCloseAndPreservesMissingFields(t *testing.T) {
	view := researchdata.NewStore()
	for _, name := range []string{"industry.daily", "size.daily"} {
		if err := view.RegisterDataset(researchdata.DatasetSpec{ID: name, Kind: researchdata.KindReference, Source: "test", Version: "1"}); err != nil {
			t.Fatal(err)
		}
	}
	closeAt := time.Date(2025, 1, 6, 15, 0, 0, 0, protocol.Location())
	rows := []researchdata.Record{
		{Dataset: "industry.daily", Code: "sh600000", Key: "class", Source: "test",
			EventAt: closeAt, AvailableAt: closeAt, Attributes: map[string]string{"industry": "金融"}},
		{Dataset: "industry.daily", Code: "sh600000", Key: "class", Source: "test", Revision: "1",
			EventAt: closeAt, AvailableAt: closeAt.Add(time.Minute), Attributes: map[string]string{"industry": "制造"}},
		{Dataset: "size.daily", Code: "sh600000", Key: "size", Source: "test",
			EventAt: closeAt, AvailableAt: closeAt, Values: map[string]float64{"log_float_cap": 5}},
	}
	if err := view.Ingest(rows); err != nil {
		t.Fatal(err)
	}
	provider := newPortfolioExposures(view, portfolioresearch.NeutralizeSpec{SizeDataset: "size.daily"})
	codes := []string{"sh600001", "sh600000"}
	industry, size, err := provider.Exposures("2025-01-06", codes)
	if err != nil || industry[0] != "" || !math.IsNaN(size[0]) || industry[1] != "金融" || size[1] != 5 {
		t.Fatalf("as-of exposure: %v %v %v", industry, size, err)
	}
	industry, _, err = provider.Exposures("2025-01-07", codes)
	if err != nil || industry[1] != "制造" {
		t.Fatalf("published revision not visible next day: %v %v", industry, err)
	}
}
