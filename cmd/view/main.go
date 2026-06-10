// view.go - 选股数据桌面展示工具
//
// 使用 lorca 打开 Chrome 窗口，内嵌 HTML 页面，
// 通过 WebSocket 连接 screen 服务的 /ws 接口，
// 实时展示买点和卖点数据。
//
// 用法：go run cmd/view/main.go [服务地址]
// 默认地址：localhost:8080

package main

import (
	_ "embed"
	"fmt"
	"os"

	"github.com/injoyai/goutil/oss"
	"github.com/injoyai/lorca"
)

//go:embed index.html
var html string

var configFileName = oss.UserInjoyDir("screen-stock", "config.txt")

const defaultHost = "localhost:8080"

func main() {

	lorca.Run(&lorca.Config{
		Width:   1200,
		Height:  800,
		Options: nil,
		Index:   html,
		Pages:   nil,
	}, func(app lorca.APP) error {
		// 暴露 Go 函数给 JS 调用
		app.Bind("loadAddr", func() string {
			data, err := os.ReadFile(configFileName)
			if err != nil {
				return defaultHost
			}
			return string(data)
		})
		app.Bind("saveAddr", func(addr string) {
			os.WriteFile(configFileName, []byte(addr), 0644)
		})

		// 绑定完成后，主动推送地址并触发连接
		data, _ := os.ReadFile(configFileName)
		addr := defaultHost
		if len(data) > 0 {
			addr = string(data)
		}
		app.Eval(fmt.Sprintf("startConnect('%s')", addr))

		return nil
	})

}
