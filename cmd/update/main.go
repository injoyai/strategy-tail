package main

import (
	"fmt"
	"os"

	"github.com/injoyai/strategy-tail/lib/extend"
)

func main() {
	fmt.Fprintln(os.Stderr, extend.ErrReadOnlyMarket)
	os.Exit(1)
}
