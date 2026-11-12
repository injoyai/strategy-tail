package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"

	"github.com/injoyai/strategy-tail/backtestreport"
)

func main() {
	dir := flag.String("dir", "", "回测输出目录")
	markdown := flag.String("markdown", "", "报告正文路径，默认读取输出目录中的 report.md")
	flag.Parse()
	if *dir == "" {
		flag.Usage()
		os.Exit(2)
	}
	if *markdown == "" {
		*markdown = filepath.Join(*dir, "report.md")
	}
	body, err := os.ReadFile(*markdown)
	if err != nil {
		log.Fatal(err)
	}
	if err := backtestreport.Write(*dir, string(body)); err != nil {
		log.Fatal(err)
	}
}
