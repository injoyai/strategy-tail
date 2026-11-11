package lab

import (
	"net/http"
	"net/url"

	common "github.com/injoyai/strategy-tail"
)

type marketDataStatus struct {
	State         string `json:"state"`
	Storage       string `json:"storage,omitempty"`
	BenchmarkDate string `json:"benchmarkDate,omitempty"`
	ReadError     string `json:"readError,omitempty"`
}

// Browsers on other origins cannot trigger writes to a local unauthenticated Lab.
func requireSameOrigin(w http.ResponseWriter, r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		if err != nil || u.Host != r.Host || u.Scheme != scheme {
			writeErr(w, http.StatusForbidden, "请从当前策略实验室页面操作")
			return false
		}
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeErr(w, http.StatusForbidden, "不允许跨站操作")
		return false
	}
	return true
}

// handleDataUpdateStatus keeps the existing status URL for deployed clients.
func (s *Server) handleDataUpdateStatus(w http.ResponseWriter, r *http.Request) {
	status := marketDataStatus{State: "idle"}
	if common.Pull != nil {
		status.Storage = common.Pull.StorageIdentity()
		date, err := common.Pull.LatestDayDate("sh000001")
		if err != nil {
			status.ReadError = "无法读取上证指数日线"
		} else {
			status.BenchmarkDate = date
		}
	}
	writeJSON(w, status)
}
