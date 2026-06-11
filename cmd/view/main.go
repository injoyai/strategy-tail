// view.go - 选股数据桌面展示工具
//
// 使用 lorca 打开 Chrome 窗口加载内嵌 HTML 页面，
// 通过 WebSocket 连接 screen 服务的 /ws 接口实时展示数据。
// 本地启动 HTTP 服务提供页面和地址持久化接口。
//
// 启动后地址通过界面输入并保存到 %USERPROFILE%\.injoy\screen-stock\config.txt。

package main

import (
	_ "embed"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/injoyai/goutil/g"
	"github.com/injoyai/goutil/oss"
	"github.com/injoyai/logs"
	"github.com/injoyai/lorca"
)

//go:embed index.html
var html string

// 首次启动的默认服务端地址
const defaultHost = "localhost:9090"

// 地址持久化文件
var configFileName = oss.UserInjoyDir("screen-stock", "config.txt")

func init() {
	os.MkdirAll(filepath.Dir(configFileName), os.ModePerm)
}

func main() {
	defer g.RecoverPrint(true)

	// 启动本地 HTTP 服务，随机端口
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		logs.Errf("[view] 启动本地服务失败: %v\n", err)
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(html))
	})
	// 读取保存的地址
	mux.HandleFunc("/api/addr", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(loadAddr()))
	})
	// 保存地址（POST body 为地址明文）
	mux.HandleFunc("/api/addr/save", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		saveAddr(string(body))
		w.WriteHeader(http.StatusNoContent)
	})

	go http.Serve(listener, mux)

	lorca.Run(&lorca.Config{
		Width:  1200,
		Height: 800,
		Index:  "http://" + listener.Addr().String(),
	}, nil)
}

// loadAddr 读取上次保存的服务地址，若不存在返回默认值
func loadAddr() string {
	data, err := os.ReadFile(configFileName)
	if err != nil || len(data) == 0 {
		return defaultHost
	}
	return string(data)
}

// saveAddr 持久化服务地址到本地文件
func saveAddr(addr string) {
	if err := os.WriteFile(configFileName, []byte(addr), 0644); err != nil {
		logs.PrintErr(err)
	}
}
