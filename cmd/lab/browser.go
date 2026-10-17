package main

import (
	"os/exec"
	"runtime"
	"time"

	"github.com/injoyai/logs"
)

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
