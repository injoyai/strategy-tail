package main

import (
	_ "embed"
	"os"

	"github.com/injoyai/frame/fbr"
)

//go:embed index.html
var indexHTML string

func Api(port int, useLocal bool, svc *ScreenService) error {
	s := fbr.Default(
		fbr.WithPort(port),
		fbr.WithALL("/", func(c fbr.Ctx) {
			c.Set("Content-Type", "text/html; charset=utf-8")
			if useLocal {
				bs, _ := os.ReadFile("./cmd/screen/index.html")
				c.Send(bs)
				return
			}
			c.SendString(indexHTML)
		}),
		fbr.WithALL("/ws", func(c fbr.Ctx) {
			c.Websocket(func(ws *fbr.Websocket) {
				svc.addSubscriber(ws)
				defer svc.removeSubscriber(ws)

				// 新连接立即推送当前快照
				buys, sells, trades := svc.snapshot()
				svc.sendTo(ws, buys)
				svc.sendTo(ws, sells)
				svc.sendTo(ws, trades)

				ws.DiscardRead()
			})
		}),
	)

	return s.Run()
}
