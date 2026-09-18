package main

// 策略实验室入口：go run ./cmd/lab → 启动本地 Web 服务并自动打开浏览器。
//
// 仅监听 127.0.0.1（设计文档 §10 非目标：不做鉴权/远程访问）。

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/injoyai/logs"
	"github.com/injoyai/strategy-tail/internal/lab"
)

func main() {
	// 默认仅监听 127.0.0.1（设计文档 §10 非目标：不做鉴权/远程访问）；
	// 容器部署时通过 LAB_ADDR=0.0.0.0:8765 覆盖，否则端口映射不可达。
	addr := os.Getenv("LAB_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8765"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		// 端口被占用时自动换一个（上次进程未退出的场景）
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		logs.PanicErr(err)
		addr = ln.Addr().String()
		logs.Warnf("默认端口被占用，改用 %s", addr)
	}

	url := fmt.Sprintf("http://%s", addr)
	logs.Infof("策略实验室已启动: %s", url)
	logs.Info("按 Ctrl+C 退出")

	go openBrowser(url)

	srv := &http.Server{
		Handler:           lab.NewServer().Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	logs.PanicErr(srv.Serve(ln))
}

// openBrowser 打开系统默认浏览器。
func openBrowser(url string) {
	time.Sleep(500 * time.Millisecond) // 等服务就绪
	var err error
	switch runtime.GOOS {
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = exec.Command("xdg-open", url).Start()
	}
	if err != nil {
		logs.Infof("请手动打开浏览器访问: %s", url)
	}
}
