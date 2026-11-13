# 命令入口

从项目根目录运行。Lab 和 Screen 统一读取 `pull.mysql`，配置和数据库表见 [MySQL 数据源](../docs/guides/mysql-storage.md)。`STRATEGY_TAIL_ROOT` 可指定运行时根目录。行情、基准、代码目录和研究数据由外部项目维护。

| 入口 | 用途与配置 | 本地输出 |
| --- | --- | --- |
| `go run ./cmd/lab` | 策略搭建、回测、因子与组合研究。`LAB_ADDR` 默认 `0.0.0.0:8765` | 保存策略、练习状态与报告 |
| `go run ./cmd/screen` | 读取 MySQL 最新分钟线并筛选买卖点，使用 `screen.*` 配置，HTTP 默认 9090 | 买卖点保存到 `data/database/trade.db` |
| `go run ./cmd/backtest-report -dir <报告目录>` | 读取 Markdown 正文和已导出的回测 CSV | 发布离线交互式 `report.html` |

Lab 的「选股策略」复用独立 Screen 的四套买入规则和标签，使用自身的选股配置。独立 Screen 仍提供买卖点监控、交易历史及诊断。日常策略实验可用 Lab 或[临时回测与只读数据接口](../docs/guides/ad-hoc-backtest.md)。Screen 的策略、HTTP API 和服务逻辑分别在 `strategies.go`、`api.go` 和 `screen.go`，静态资源在 `web/`。

`cmd/screen/dockerpush.sh` 使用项目根目录作为构建上下文，并推送到脚本配置的镜像仓库。运行镜像时通过环境变量或私有配置挂载提供 `pull.mysql`。
