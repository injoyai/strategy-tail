package main

import (
	_ "embed"
	"os"
	"regexp"
	"sort"

	"github.com/injoyai/frame/fbr"
	"github.com/injoyai/logs"
	"github.com/injoyai/strategy-tail/internal/chartui"
)

// codeFormat A股代码格式校验,防止任意字符串流入文件路径拼接等下游逻辑。
var codeFormat = regexp.MustCompile(`^(sh|sz|bj)?\d{6}$`)

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
		if bs, err := os.ReadFile(screenWebPath(filename)); err == nil {
			return bs
		}
	}
	return embedded
}

func maybeLocalStr(useLocal bool, filename string, embedded string) string {
	if useLocal {
		if bs, err := os.ReadFile(screenWebPath(filename)); err == nil {
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

		fbr.WithGET("/kline.js", func(c fbr.Ctx) {
			c.Set("Content-Type", "application/javascript; charset=utf-8")
			c.SendString(chartui.JavaScript())
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
			resp := svc.response(TypeHistory, trades)
			c.JSON(resp)
		}),

		// 诊断
		fbr.WithGET("/api/diagnose", func(c fbr.Ctx) {
			code := c.Query("code")
			strategy := c.Query("strategy")
			if !codeFormat.MatchString(code) {
				c.Status(400).JSON(map[string]any{"error": "无效的股票代码"})
				return
			}
			resp, err := svc.Diagnose(code, strategy)
			if err != nil {
				// 底层错误可能含路径等实现细节,仅记录日志,前端返回通用提示。
				logs.Errf("[诊断] %s 失败: %v", code, err)
				c.Status(500).JSON(map[string]any{"error": "诊断失败,请稍后重试"})
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
				svc.sendTo(ws, TypeBuy, buys)
				svc.sendTo(ws, TypeSell, sells)

				ws.DiscardRead()
			})
		}),
	)

	return s.Run()
}
