package chartui

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddedChartRuntime(t *testing.T) {
	for _, name := range []string{"kline.js", "vendor/klinecharts-10.1.0.min.js", "vendor/LICENSE", "vendor/NOTICE", "vendor/LICENSE-lightweight-charts"} {
		response := httptest.NewRecorder()
		Handler().ServeHTTP(response, httptest.NewRequest("GET", "/"+name, nil))
		if response.Code != 200 || response.Body.Len() == 0 {
			t.Fatalf("asset %s: %d", name, response.Code)
		}
	}
	for _, text := range []string{"KLineChart 10.1.0", "Apache License", "TradingView", "StrategyKline"} {
		if !strings.Contains(Scripts(), text) {
			t.Fatalf("standalone runtime missing %s", text)
		}
	}
	if strings.Count(Scripts(), "</script>") != 1 {
		t.Fatal("embedded runtime must not contain a script-closing sequence")
	}
}
