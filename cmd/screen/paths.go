package main

import (
	"path/filepath"

	common "github.com/injoyai/strategy-tail"
)

func screenDBPath() string {
	return common.ResolveRuntimePath(filepath.Join("data", "database", "trade.db"))
}

func screenWebPath(filename string) string {
	return common.ResolveRuntimePath(filepath.Join("cmd", "screen", "web", filename))
}
