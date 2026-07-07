package main

import (
	_ "embed"
	"os"
	"sort"
	"time"

	"github.com/injoyai/frame/fbr"
)

//go:embed web/index.html
var indexHTML string

//go:embed web/style.css
var styleCSS []byte

//go:embed web/app.js
var appJS []byte

//go:embed web/diagnose.js
var diagnoseJS []byte

// maybeLocal 本地开发模式读取磁盘文件，否则用 embed 内容
func maybeLocal(useLocal bool, filename string, embedded []byte) []byte {
	if useLocal {
		if bs, err := os.ReadFile("./cmd/screen/web/" + filename); err == nil {
			return bs
		}
	}
	return embedded
}

func maybeLocalStr(useLocal bool, filename string, embedded string) string {
	if useLocal {
		if bs, err := os.ReadFile("./cmd/screen/web/" + filename); err == nil {
			return string(bs)
		}
	}
	return embedded
}

func Api(port int, useLocal bool, svc *ScreenService) error {
	s := fbr.Default(
		fbr.WithPort(port),

		// ── 页面与静态资源 ──
		fbr.WithGET("/", func(c fbr.Ctx) {
			c.Set("Content-Type", "text/html; charset=utf-8")
			c.SendString(maybeLocalStr(useLocal, "index.html", indexHTML))
		}),
		fbr.WithGET("/style.css", func(c fbr.Ctx) {
			c.Set("Content-Type", "text/css; charset=utf-8")
			c.Send(maybeLocal(useLocal, "style.css", styleCSS))
		}),
		fbr.WithGET("/app.js", func(c fbr.Ctx) {
			c.Set("Content-Type", "application/javascript; charset=utf-8")
			c.Send(maybeLocal(useLocal, "app.js", appJS))
		}),
		fbr.WithGET("/diagnose.js", func(c fbr.Ctx) {
			c.Set("Content-Type", "application/javascript; charset=utf-8")
			c.Send(maybeLocal(useLocal, "diagnose.js", diagnoseJS))
		}),

		// ── HTTP API ──

		// 策略列表
		fbr.WithGET("/api/strategies", func(c fbr.Ctx) {
			type strategyItem struct {
				Key  string   `json:"key"`
				Name string   `json:"name"`
				Tags []string `json:"tags"`
			}
			out := make([]strategyItem, 0, len(svc.Strategies))
			for _, st := range svc.Strategies {
				tags := make([]string, 0, len(st.Tags))
				for name := range st.Tags {
					tags = append(tags, name)
				}
				sort.Strings(tags)
				out = append(out, strategyItem{Key: st.Key, Name: st.Name, Tags: tags})
			}
			c.JSON(out)
		}),

		// 历史买卖点（全量，前端过滤）
		fbr.WithGET("/api/history", func(c fbr.Ctx) {
			_, _, trades := svc.snapshot()
			now := time.Now().Format(time.DateTime)
			resp := svc.buildHistoryResponse(trades, now)
			c.JSON(resp)
		}),

		// 诊断
		fbr.WithGET("/api/diagnose", func(c fbr.Ctx) {
			code := c.Query("code")
			strategy := c.Query("strategy")
			resp, err := svc.Diagnose(code, strategy)
			if err != nil {
				c.JSON(map[string]any{"error": err.Error()})
				return
			}
			c.JSON(resp)
		}),

		// ── 实时 WebSocket（买点/卖点）──
		fbr.WithALL("/ws", func(c fbr.Ctx) {
			c.Websocket(func(ws *fbr.Websocket) {
				svc.addSubscriber(ws)
				defer svc.removeSubscriber(ws)

				// 新连接立即推送当前快照（买点+卖点，历史走HTTP）
				buys, sells, _ := svc.snapshot()
				svc.sendTo(ws, buys)
				svc.sendTo(ws, sells)

				ws.DiscardRead()
			})
		}),
	)

	return s.Run()
}
