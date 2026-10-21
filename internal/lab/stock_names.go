package lab

import (
	"net/http"

	common "github.com/injoyai/strategy-tail"
)

// 名称仅来自当前行情目录，不补写历史报告，也不因页面读取触发行情初始化。
func (s *Server) handleStockNames(w http.ResponseWriter, r *http.Request) {
	names := make(map[string]string)
	if common.Manage != nil && common.Manage.Codes != nil {
		codes := common.Manage.Codes
		for _, code := range append(codes.GetStockCodes(), codes.GetIndexCodes()...) {
			if name := codes.GetName(code); name != "" {
				names[code] = name
			}
		}
	}
	writeJSON(w, names)
}
