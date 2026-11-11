package lab

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/v2/extend/metadata"
	"github.com/injoyai/tdx/v2/extend/model"
)

func TestStockNamesAPI(t *testing.T) {
	previous := common.Manage
	t.Cleanup(func() { common.Manage = previous })
	catalog, err := metadata.NewCodes([]*model.CodeModel{
		{Exchange: "sh", Code: "600000", Name: "浦发银行"},
		{Exchange: "sz", Code: "000001", Name: "平安银行"},
		{Exchange: "sh", Code: "000001", Name: "上证指数"},
		{Exchange: "sh", Code: "600001"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{mux: http.NewServeMux()}
	s.registerRoutes()
	for _, tc := range []struct {
		name   string
		manage *extend.MarketData
		want   map[string]string
	}{
		{"未初始化", nil, map[string]string{}},
		{"目录未就绪", &extend.MarketData{}, map[string]string{}},
		{"按完整代码区分股票和指数", &extend.MarketData{Codes: catalog}, map[string]string{"sh600000": "浦发银行", "sz000001": "平安银行", "sh000001": "上证指数"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			common.Manage = tc.manage
			response := httptest.NewRecorder()
			s.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/stock-names", nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d", response.Code)
			}
			var got map[string]string
			if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for code, name := range tc.want {
				if got[code] != name {
					t.Errorf("%s = %q, want %q", code, got[code], name)
				}
			}
			if common.Manage != tc.manage {
				t.Fatal("读取名称不得初始化行情")
			}
		})
	}
}
