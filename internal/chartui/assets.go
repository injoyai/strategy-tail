// Package chartui owns the K-line runtime shared by Lab, screening and HTML exports.
package chartui

import (
	"embed"
	"net/http"
)

//go:embed kline.js vendor/*
var files embed.FS

// Handler serves the fixed, embedded runtime and its license notices.
func Handler() http.Handler { return http.FileServer(http.FS(files)) }

// Scripts embeds the same runtime in standalone reports, without a CDN dependency.
func Scripts() string {
	return "<script>" + JavaScript() + "</script>"
}

// JavaScript is also used by the screening service's existing static router.
func JavaScript() string {
	vendor, _ := files.ReadFile("vendor/klinecharts-10.1.0.min.js")
	adapter, _ := files.ReadFile("kline.js")
	license, _ := files.ReadFile("vendor/LICENSE")
	notice, _ := files.ReadFile("vendor/NOTICE")
	upstreamLicense, _ := files.ReadFile("vendor/LICENSE-lightweight-charts")
	return "/*! KLineChart 10.1.0\n" + string(notice) + "\n" + string(license) + "\n" + string(upstreamLicense) + "*/\n" + string(vendor) + "\n" + string(adapter)
}
