# 文档导航

项目启动与配置见[项目 README](../README.md)，目录职责和模块入口见[项目目录说明](guides/project-layout.md)。

## 使用与开发指南

- [项目目录与代码入口](guides/project-layout.md)
- [因子研究与回测搭建：概念与关系](guides/research-and-backtest-concepts.md)
- [因子开发与接入规范](guides/factor-development.md)
- [v2 多因子组合研究发布文档](guides/v2-multifactor-portfolio.md)（2026-09-19 冻结快照，保留当时的 hash、门禁结果和限制）

## 根目录约定

- [协作规范](../AGENTS.md)、[项目工程规则](../PROJECT_RULES.md)
- [业务词汇表](../CONTEXT.md)
- [界面设计上下文](../DESIGN.md)、[交互契约](../UX-CONTRACT.md)
- [项目核心记忆](../MEMORY.md)

## 设计文档

以下文件记录各阶段的设计与约束；实现状态以文件标注、当前源码和验证结果为准。

| 主题 | 设计 |
|---|---|
| SQLite 买点持久化 | [2026-06-12](specs/2026-06-12-sqlite-buy-points-design.md) |
| 策略可视化 | [2026-07-02](specs/2026-07-02-strategy-visualization-design.md) |
| 均线回踩与地量反转 | [2026-07-04](specs/2026-07-04-ma-pullback-volume-design.md) |
| 风控作为 Seller | [2026-07-06](specs/2026-07-06-risk-as-seller-design.md) |
| 买入信号未来收益分析 | [2026-07-09](specs/2026-07-09-forward-return-analysis-design.md) |
| 命中点 K 线可视化 | [2026-07-11](specs/2026-07-11-future-hit-visual-design.md) |
| 策略实验室 | [2026-09-05](specs/2026-09-05-strategy-lab-design.md) |
| 因子框架 | [2026-09-07](specs/2026-09-07-factor-framework-design.md) |
| 因子 Web 工作流 | [2026-09-16](specs/2026-09-16-factor-web-mvp-design.md) |
| 候选因子独立页面 | [2026-09-17](specs/2026-09-17-candidate-page-design.md) |
| 因子分组数与值域展示 | [2026-09-17](specs/2026-09-17-factor-analysis-group-counts-design.md) |
| 候选因子保存与复用 | [2026-09-17](specs/2026-09-17-factor-candidate-library-design.md) |
| 专业因子验证 v1 | [2026-09-18](specs/2026-09-18-factor-validation-v1-design.md) |
| 多因子组合研究 v2 | [2026-09-18](specs/2026-09-18-multifactor-portfolio-v2-design.md) |
| 前瞻纸面交易与模型治理 v3 | [2026-09-18](specs/2026-09-18-prospective-paper-trading-v3-design.md) |
| 自由组合策略搭建器 | [2026-10-13](specs/2026-10-13-strategy-builder-design.md) |

## 实施计划与阶段记录

计划中的任务和示例命令保留当时语义，不代表当前待办清单；引用路径已随目录迁移更新。

| 主题 | 计划或记录 |
|---|---|
| SQLite 买点持久化 | [2026-06-12](plans/2026-06-12-sqlite-buy-points.md) |
| 风控作为 Seller | [2026-07-06](plans/2026-07-06-risk-as-seller.md) |
| 未来收益分析 | [2026-07-09](plans/2026-07-09-forward-return-analysis.md) |
| 因子框架 | [2026-09-07](plans/2026-09-07-factor-framework.md) |
| 因子分析时间范围 | [2026-09-16](plans/2026-09-16-factor-analysis-time-range.md) |
| 分组可解释性与固定区间研究 | [2026-09-16](plans/2026-09-16-factor-group-interpretability.md) |
| 因子 Web 工作流 | [2026-09-16](plans/2026-09-16-factor-web-mvp.md) |
| 候选因子独立页面 | [2026-09-17](plans/2026-09-17-candidate-page.md) |
| 因子分组数与值域展示 | [2026-09-17](plans/2026-09-17-factor-analysis-group-counts.md) |
| 候选因子保存与复用 | [2026-09-17](plans/2026-09-17-factor-candidate-library.md) |
| 专业因子验证 v1 | [2026-09-18](plans/2026-09-18-factor-validation-v1.md) |
| v2 准入与兼容夹具 | [2026-09-18 Task 0](plans/2026-09-18-multifactor-portfolio-v2-task0-admission.md) |
| 多因子组合研究 v2 | [2026-09-18](plans/2026-09-18-multifactor-portfolio-v2.md) |
| 前瞻纸面交易与模型治理 v3 | [2026-09-18](plans/2026-09-18-prospective-paper-trading-v3.md) |

## 审查与复核

- [2026-10-17 代码审查复核报告](reviews/code-review-2026-10-17.md)：保留原 51 个编号及首次复核快照，第 6 节记录后续优化与验证。

新文档按用途放入 `guides/`、`specs/`、`plans/` 或 `reviews/`，并在本页登记。源码、持久状态和生成报告分别留在代码目录、`data/` 与 `output/`。
