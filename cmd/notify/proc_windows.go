//go:build windows

package main

import "syscall"

// hiddenSysProcAttr 隐藏 PowerShell 控制台窗口，避免每次提醒闪出黑框。
func hiddenSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true}
}
