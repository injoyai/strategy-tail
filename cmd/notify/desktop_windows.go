//go:build windows

package main

import (
	"context"
	"log"
	"os/exec"
	"strings"

	"fyne.io/systray"
)

func runDesktop(ctx context.Context, cancel context.CancelFunc, cfg config, cursor int64) {
	systray.Run(func() {
		systray.SetIcon(trayIcon())
		systray.SetTooltip("策略卖点提醒 · 连接中")

		status := systray.AddMenuItem("状态：连接中", "")
		status.Disable()
		settings := systray.AddMenuItem("设置服务器地址", "")
		open := systray.AddMenuItem("打开 Lab 页面", "")
		logs := systray.AddMenuItem("打开日志", "")
		systray.AddSeparator()
		quit := systray.AddMenuItem("退出", "")

		cfg.onStatus = func(text string) {
			status.SetTitle("状态：" + text)
			systray.SetTooltip("策略卖点提醒 · " + text)
		}
		go run(ctx, cfg, cursor)
		go func() {
			for {
				select {
				case <-settings.ClickedCh:
					go configureServer(cfg)
				case <-open.ClickedCh:
					openBrowser(cfg.server.Get())
				case <-logs.ClickedCh:
					openLog(cfg.statePath)
				case <-quit.ClickedCh:
					cancel()
					systray.Quit()
					return
				}
			}
		}()
	}, cancel)
}

func configureServer(cfg config) {
	entered, err := promptServer(cfg.server.Get())
	if err != nil {
		return // 用户取消
	}
	target := strings.TrimSpace(entered)
	if target == "" {
		notifyDesktop("策略卖点提醒 · 地址无效", "服务器地址不能为空")
		return
	}
	if _, err := eventsURL(target, 0); err != nil {
		notifyDesktop("策略卖点提醒 · 地址无效", target)
		return
	}
	if err := saveServer(cfg.statePath, target); err != nil {
		log.Printf("保存配置失败: %v", err)
		notifyDesktop("策略卖点提醒 · 保存失败", err.Error())
		return
	}
	cfg.status("正在切换服务器")
	cfg.server.Set(target)
	notifyDesktop("策略卖点提醒 · 已保存", target)
}

func openBrowser(server string) {
	target := server
	if !strings.Contains(target, "://") {
		target = "http://" + target
	}
	if err := exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start(); err != nil {
		log.Printf("打开页面失败: %v", err)
	}
}

func openLog(statePath string) {
	if err := exec.Command("notepad.exe", logFilePath(statePath)).Start(); err != nil {
		log.Printf("打开日志失败: %v", err)
	}
}
