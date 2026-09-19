package main

import (
	"path/filepath"

	common "github.com/injoyai/strategy-tail"
)

func reportOutputDir() string {
	return common.ResolveRuntimePath(filepath.Join("output", "market-regime"))
}
