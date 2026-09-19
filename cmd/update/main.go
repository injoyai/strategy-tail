package main

import (
	"github.com/injoyai/logs"
	common "github.com/injoyai/strategy-tail"
)

func main() {
	common.MustInitialize()
	logs.PanicErr(common.Update())
}
