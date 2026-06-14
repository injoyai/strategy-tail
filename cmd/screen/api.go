package main

import (
	_ "embed"
	"encoding/json"

	"github.com/injoyai/frame/fbr"
)

//go:embed index.html
var indexHTML string

func Api(port int, svc *ScreenService) error {
	s := fbr.Default(
		fbr.WithPort(port),
		fbr.WithALL("/", func(c fbr.Ctx) {
			c.Set("Content-Type", "text/html; charset=utf-8")
			c.SendString(indexHTML)
		}),
		fbr.WithALL("/ws", func(c fbr.Ctx) {
			c.Websocket(func(ws *fbr.Websocket) {
				svc.addSubscriber(ws)
				defer svc.removeSubscriber(ws)

				// 新连接立即推送当前快照
				buys, sells, history := svc.snapshot()
				if buys != nil {
					if data, err := json.Marshal(buys); err == nil {
						ws.WriteText(string(data))
					}
				}
				if sells != nil {
					if data, err := json.Marshal(sells); err == nil {
						ws.WriteText(string(data))
					}
				}
				if history != nil {
					if data, err := json.Marshal(history); err == nil {
						ws.WriteText(string(data))
					}
				}

				ws.DiscardRead()
			})
		}),
	)

	return s.Run()
}
