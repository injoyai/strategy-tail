# 命令入口

从项目根目录运行。Lab 读取 `pull.mysql`，配置和数据库表见 [MySQL 数据源](../docs/guides/mysql-storage.md)。`STRATEGY_TAIL_ROOT` 可指定运行时根目录。行情、基准、代码目录和研究数据由外部项目维护。

| 入口 | 用途与配置 | 本地输出 |
| --- | --- | --- |
| `go run ./cmd/lab` | 策略搭建、回测、因子与组合研究、选股。`LAB_ADDR` 默认 `0.0.0.0:8765` | 保存策略、练习状态、交易账本与报告 |
| `go run ./cmd/backtest-report -dir <报告目录>` | 读取 Markdown 正文和已导出的回测 CSV | 发布离线交互式 `report.html` |

买卖点监控、交易历史及诊断统一使用 [Lab 选股策略](../docs/guides/stock-screening.md)。日常策略实验也可用[临时回测与只读数据接口](../docs/guides/ad-hoc-backtest.md)。
