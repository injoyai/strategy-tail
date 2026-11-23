//go:build !windows

package main

import "syscall"

func hiddenSysProcAttr() *syscall.SysProcAttr {
	return nil
}
