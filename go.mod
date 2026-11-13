module github.com/injoyai/strategy-tail

go 1.25.4

// Use the local v2 SDK; MySQL storage adapters live in this application.
replace github.com/injoyai/tdx/v2 => ../tdx

require (
	github.com/fasthttp/websocket v1.5.12
	github.com/glebarez/go-sqlite v1.22.0
	github.com/go-sql-driver/mysql v1.8.1
	github.com/injoyai/bar v0.0.12
	github.com/injoyai/conv v1.2.8
	github.com/injoyai/goutil v1.2.34
	github.com/injoyai/logs v1.0.12
	github.com/injoyai/tdx/v2 v2.0.0
	github.com/xuri/excelize/v2 v2.11.0
	golang.org/x/sys v0.46.0
)

require (
	filippo.io/edwards25519 v1.1.0 // indirect
	github.com/andybalholm/brotli v1.2.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/fatih/color v1.19.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/grafov/m3u8 v0.12.1 // indirect
	github.com/injoyai/base v1.2.24 // indirect
	github.com/injoyai/ios v1.2.6 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/compress v1.18.1 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/ncruces/go-strftime v0.1.9 // indirect
	github.com/pelletier/go-toml/v2 v2.2.4 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/richardlehane/mscfb v1.0.7 // indirect
	github.com/richardlehane/msoleps v1.0.6 // indirect
	github.com/savsgio/gotils v0.0.0-20240704082632-aef3928b8a38 // indirect
	github.com/tiendc/go-deepcopy v1.7.2 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasthttp v1.68.0 // indirect
	github.com/xuri/efp v0.0.1 // indirect
	github.com/xuri/nfp v0.0.2-0.20250530014748-2ddeb826f9a9 // indirect
	golang.org/x/crypto v0.53.0 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/text v0.38.0 // indirect
	gopkg.in/ini.v1 v1.67.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	modernc.org/libc v1.55.3 // indirect
	modernc.org/mathutil v1.6.0 // indirect
	modernc.org/memory v1.8.0 // indirect
	modernc.org/sqlite v1.28.0 // indirect
	xorm.io/xorm v1.3.11 // indirect
)
