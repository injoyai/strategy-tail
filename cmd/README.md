# 命令入口

从项目根目录运行。Lab 读取 `pull.mysql`，配置和数据库表见 [MySQL 数据源](../docs/guides/mysql-storage.md)。`STRATEGY_TAIL_ROOT` 可指定运行时根目录。行情、基准、代码目录和研究数据由外部项目维护。

| 入口 | 用途与配置 | 本地输出 |
| --- | --- | --- |
| `go run ./cmd/lab` | 策略搭建、回测、因子与组合研究、选股。`LAB_ADDR` 默认 `0.0.0.0:8765` | 保存策略、练习状态、交易账本与报告 |
| `go run ./cmd/backtest-report -dir <报告目录>` | 读取 Markdown 正文和已导出的回测 CSV | 发布离线交互式 `report.html` |
| `go run ./cmd/notify -server http://<家里地址>` | Windows 卖点桌面提醒，连接 Lab 的 `/api/screen/events` | 提醒进度文件 |

买卖点监控、交易历史及诊断统一使用 [Lab 选股策略](../docs/guides/stock-screening.md)。日常策略实验也可用[临时回测与只读数据接口](../docs/guides/ad-hoc-backtest.md)。

## Windows 卖点提醒

`cmd/notify` 是独立客户端，可在外网或公司通过已组建的局域网连接家里的 Lab，收到卖点事件时弹出 Windows 通知。程序以托盘图标运行，自动重连，并在本地记录事件位置，重启后不会重复提醒。首次运行会弹出设置窗口，填写一次服务器地址后保存到用户配置目录。

部署镜像会同时构建该客户端，也可在选股策略页「策略列表」右上角的「下载提醒工具」按钮直接下载。

```bash
# 交叉编译（在 macOS/Linux 上生成 Windows 可执行文件）
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags '-s -w -H windowsgui' -o bin/strategy-notify.exe ./cmd/notify
```

```bat
:: 首次运行会弹出设置窗口，填写家里 Lab 的局域网地址
bin\strategy-notify.exe

:: 也可以直接用参数指定地址（会优先于已保存配置）
bin\strategy-notify.exe -server http://192.168.1.10
```

托盘菜单包含连接状态、设置服务器地址、打开 Lab 页面、打开日志和退出。修改地址后会自动断开旧连接并重连，无需重启程序。配置保存在当前用户配置目录的 `strategy-notify/config.json`。由于是 GUI 程序没有控制台，日志写入同目录的 `notify.log`，可直接从托盘菜单打开。

| 参数 | 说明 |
| --- | --- |
| `-server` | 可选，家里 Lab 地址；未指定时读取 `config.json`，首次运行弹出设置窗口。支持 `http://`、`https://` 或省略协议 |
| `-state` | 提醒进度文件，默认在当前用户配置目录的 `strategy-notify/cursor` |
| `-after` | 覆盖初始事件位置，`-1` 表示读取进度文件 |
| `-retry` | 断线重连间隔，默认 `3s` |
| `-replay` | 首次运行补发已有卖点；默认只提醒启动后的新卖点 |
| `-tray=false` | 不显示托盘图标，只在后台运行 |
| `-notify=false` | 不弹桌面通知，只写日志，便于排查连接问题 |

需要开机自启时，在 `shell:startup` 目录放一个指向该 exe 的快捷方式即可，地址会从已保存配置读取。启动后托盘出现红色圆环图标即表示程序在运行。
