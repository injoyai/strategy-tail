package main

import (
	"github.com/injoyai/frame/fbr"
)

func main() {

	s := fbr.Default()
	s.POST("/screen", func(c fbr.Ctx) {
		c.Websocket(func(ws *fbr.Websocket) {

		})
	})
	s.Run()
}
