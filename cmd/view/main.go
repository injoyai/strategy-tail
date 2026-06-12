package main

import (
	"github.com/injoyai/lorca"
)

func main() {
	lorca.Run(&lorca.Config{
		Width:  1400,
		Height: 800,
		Index:  "http://localhost:9090",
	})
}
