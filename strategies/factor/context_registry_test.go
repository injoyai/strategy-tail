package factor

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

func TestBuildContextPreservesCatalogFactor(t *testing.T) {
	legacy := Build("momentum", 2)
	contextual := BuildContext("momentum", 2)
	if legacy == nil || contextual == nil {
		t.Fatal("known factor was not built")
	}
	d1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	dks := extend.Klines{
		{Kline: &protocol.Kline{Time: d1, Close: protocol.Yuan(10)}},
		{Kline: &protocol.Kline{Time: d1.AddDate(0, 0, 1), Close: protocol.Yuan(11)}},
		{Kline: &protocol.Kline{Time: d1.AddDate(0, 0, 2), Close: protocol.Yuan(12)}},
	}
	want := legacy.Value("sh600000", dks)
	got := contextual.ValueAt(core.FactorContext{Code: "sh600000", AsOf: dks[len(dks)-1].Time, Klines: dks})
	if got != want {
		t.Fatalf("context value = %v, legacy value = %v", got, want)
	}
}

func TestBuildContextUnknownKind(t *testing.T) {
	if got := BuildContext("unknown", 0); got != nil {
		t.Fatalf("BuildContext(unknown) = %#v, want nil", got)
	}
}

func TestBuildContextSupportsContextOnlyCatalogEntry(t *testing.T) {
	old := registry
	registry = append(registry, entry{
		Kind: "test_context", Default: 1,
		NewContext: func(int) core.ContextFactor {
			return 最新字段{Dataset: "fundamental.daily", Field: "pe_ttm", Label: "市盈率TTM"}
		},
		Description: "test", Category: "test", ParameterLabel: "none", Unit: "multiple",
		Example: "test", ImplementationVersion: 1,
	})
	t.Cleanup(func() { registry = old })

	if got := Build("test_context", 0); got != nil {
		t.Fatalf("legacy Build() = %#v, want nil for context-only factor", got)
	}
	if got := BuildContext("test_context", 0); got == nil || got.Name() != "市盈率TTM" {
		t.Fatalf("BuildContext() = %#v, want context factor", got)
	}
	entries := All()
	if got := entries[len(entries)-1].Name; got != "市盈率TTM" {
		t.Fatalf("catalog name = %q, want 市盈率TTM", got)
	}
}
