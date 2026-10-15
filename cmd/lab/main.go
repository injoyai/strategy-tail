package main

// 策略实验室入口：go run ./cmd/lab → 启动本地 Web 服务并自动打开浏览器。
//
// 默认监听 0.0.0.0，可通过 LAB_ADDR 覆盖。

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/injoyai/logs"
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/internal/lab"
)

func main() {
	common.MustInitialize()
	addr := os.Getenv("LAB_ADDR")
	if addr == "" {
		addr = "0.0.0.0:8765"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		// 端口被占用时自动换一个（上次进程未退出的场景）
		host, _, splitErr := net.SplitHostPort(addr)
		logs.PanicErr(splitErr)
		ln, err = net.Listen("tcp", net.JoinHostPort(host, "0"))
		logs.PanicErr(err)
		addr = ln.Addr().String()
		logs.Warnf("默认端口被占用，改用 %s", addr)
	}

	url := fmt.Sprintf("http://%s", addr)
	logs.Infof("策略实验室已启动: %s", url)
	logs.Info("按 Ctrl+C 退出")

	go openBrowser(url)

	server := lab.NewServer()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.RunDataUpdateScheduler(ctx)
	srv := &http.Server{
		Handler:           server.Handler(),
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
