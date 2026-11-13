package lab

import (
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/injoyai/tdx/v2/protocol"
)

func (s *Server) handleScreenEvents(w http.ResponseWriter, r *http.Request) {
	if !requireSameOrigin(w, r) {
		return
	}
	after, err := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	if r.URL.Query().Get("after") != "" && (err != nil || after < 0) {
		writeErr(w, http.StatusBadRequest, "无效的卖点事件位置")
		return
	}
	upgrade := websocket.Upgrader{HandshakeTimeout: 5 * time.Second}
	conn, err := upgrade.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(256)
	conn.SetReadDeadline(time.Now().Add(time.Minute))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(time.Minute)) })
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		events, err := s.screen.readSaleEvents(after)
		if err != nil {
			return
		}
		for _, event := range events {
			if conn.SetWriteDeadline(time.Now().Add(5*time.Second)) != nil || conn.WriteJSON(event) != nil {
				return
			}
			after = event.ID
		}
		select {
		case <-done:
			return
		case <-r.Context().Done():
			return
		case <-ping.C:
			if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)) != nil {
				return
			}
		case <-tick.C:
		}
	}
}

func (s *screenService) readSaleEvents(after int64) ([]screenSaleEvent, error) {
	if _, err := os.Stat(s.ledgerPath()); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	db, err := openScreenLedger(s.ledgerPath())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	day := s.now().In(protocol.Location()).Format(time.DateOnly)
	return screenEvents(db, after, day)
}
