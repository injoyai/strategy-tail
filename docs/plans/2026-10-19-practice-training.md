# 题库训练实现与存储约定

依据：[定稿需求](../specs/2026-10-19-practice-training-requirements.md)。

## 存储与兼容

- 既有 `data/lab/practice/practice_*/` 保持原格式，作为历史单题或重练记录；没有训练 ID 的旧记录不推断归组。
- 新模型存于 `data/lab/practice/training-v1/`，由单个 Lab 服务实例写入。未引入数据库、依赖或旧数据迁移。
- `banks/bank_*/bank.json` 冻结买入策略、版本、费用、年份、覆盖和每股累计题数。`stock-N.json` 保存该股一份行情及全部信号索引。
- 扫描仍至多四股并行；每批合并时逐股写盘，不保留全市场行情和全部问题对象。只有最终原子写出的 `ready` 清单可用于训练。加载和最终保存没有估算百分比；扫描进度为已处理股票数（含失败、空数据）。
- 准备中发生取消、读取/求值失败或存储错误，不发布不完整的可用库；单股扫描异常继续计入覆盖失败。构建快照/写盘错误终止整库准备。进程重启把残留 preparing 库标为中断。未写出初始清单的空目录忽略；存在但损坏的清单明确报错。
- `trainings/training_*/training.json` 包含当前题、正式作答、随机抽题映射、命令回执和起止边界。每次动作在同一 Store 锁内读取、校验并原子提交整个训练；崩溃不会只提交交易或只提交抽题。已完成题的统计行在结束时冻结。
- 随机抽题使用稀疏 Fisher–Yates；内存/状态量随实际抽过的题数增长，不为全库构造随机排列。题库清单随股票数增长，单股信号随该股行情长度增长。训练 JSON 随实际操作和作答增长，面向个人单服务使用，不声称多实例写入或无限训练规模下常数开销。

## 接口

| 接口 | 用途 |
| --- | --- |
| GET / POST `/api/practice-banks` | 题库目录 / 后台建立题库，POST 接收 strategyId、strategyVersion、fromYear、toYear、requestId |
| GET `/api/practice-banks/{id}` | 准备/失败/可用状态；不下发股票清单或信号索引 |
| GET / POST `/api/practice-trainings` | 训练历史 / 从 bankId 开始独立训练，按 requestId 幂等 |
| GET `/api/practice-trainings/{id}` | 权威训练版本、计数及当前匿名题目，只含已揭示行情 |
| POST `/api/practice-trainings/{id}/actions` | action、questionId、commandId、expectedRevision；next 为下一交易日，next_question 为下一题，end_training 为结束训练 |
| GET `/api/practice-trainings/{id}/report` | 仅已结束训练可读的正式报告 |
| GET `/api/practice-trainings/{id}/questions/{question}` | 仅已结束题可钻取的题目状态 |
| GET `/api/practice-trainings/{id}/questions/{question}/review` | 该已结束题的单题报告和限定复盘行情 |
| POST `/api/practice-trainings/{id}/questions/{question}/retry` | 按 requestId 幂等建立独立重练，不更改训练 |

沿用严格 JSON 解码、请求体限额和同源写入检查。动作必须匹配当前题和训练 revision；同命令重试返回当前持久状态，变更动作/题目的同键请求拒绝。不同键的重复下一题由题目 ID 与版本拦截。

## 报告与页面

聚合先按未交易、真实平仓、未平仓分流；只把真实平仓传给 `core.Stats`。胜率含打平分母，平均交易收益率按各题买入成本归一后算术平均。无平仓时返回 null。已实现、未平仓估值/浮盈亏和真实费用分别列出，费用不重复扣减；额外复盘日不进入成绩。

页面入口改为题库；训练工具栏提供“结束训练并生成报告”，题目工具栏继续保留“结束本题”。完成最后一题自动出训练报告，其余题结束后可复盘或直接开始下一题。训练、题库、旧单题分别使用 `?t=`、`?bank=`、`?p=` 恢复；切页不结束训练。报告下载复用页面同一服务端响应及格式化函数，不另算财务指标。

兼容旧单题 API 与深链接；新入口不再为每题重扫。策略更新后需要另建题库。历史单题与重练的列表入口独立保留。
