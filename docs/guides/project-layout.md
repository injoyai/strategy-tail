# 项目目录与代码入口

源码按现有 Go 包边界组织；单元测试和夹具留在对应包的 `*_test.go`、`testdata/` 中。查阅资料从[文档导航](../README.md)开始。

## 目录职责

```text
strategy-tail/
├── cmd/                       # 命令入口
├── core/                      # 交易契约、回测与公共统计
├── strategies/
│   ├── buy/                   # 买入条件与组合子
│   ├── sell/                  # 卖出条件与独立风控
│   ├── factor/                # 因子计算、目录与工厂
│   ├── util/                  # 策略共用计算
│   └── script/                # 兼容脚本及 Lab 保存路径
├── researchdata/              # PIT 数据契约、视图、存储与供应商
├── lib/extend/                # TDX 行情与本地 SQLite 适配
├── internal/
│   ├── lab/                   # Web 实验室与任务编排
│   │   ├── builder/           # 搭建器目录、规则校验与条件构建
│   │   ├── factorstats/       # 因子统计与中性化纯计算
│   │   └── script/            # 脚本解释器、符号表与单元测试
│   ├── portfolioresearch/     # 组合研究算法
│   └── researchrun/           # 研究数据加载与执行
├── docs/
│   ├── README.md             # 文档导航
│   ├── guides/               # 使用与开发指南
│   ├── specs/                # 设计文档
│   ├── plans/                # 实施计划与阶段记录
│   └── reviews/              # 审查与复核报告
├── config/                    # 运行配置
├── tests/                     # 跨语言、部署等外围测试
├── data/                      # 本地数据库与持久状态
├── output/                    # 生成报告与导出文件
└── bin/                       # 编译产物
```

根包的 [common.go](../../common.go) 负责公共装配，[config.go](../../config.go) 负责统一配置加载与回测配置读取，[runtime_path.go](../../runtime_path.go) 负责运行时根目录，[local_codes.go](../../local_codes.go) 负责枚举本地行情代码。当前保留的五个命令及启动方式见[命令说明](../../cmd/README.md)。

## Lab 模块入口

`internal/lab/` 负责服务装配、任务编排和存储，独立逻辑按三条边界归入 `builder`、`factorstats`、`script`。父包中的 `builder.go`、`factor_stats.go`、`interp.go` 保留已有类型及调用入口。

三个子包均不反向导入 `lab`：`builder` 依赖核心与策略组件；`factorstats` 只依赖标准库；`script` 依赖核心、策略组件和 Yaegi。原 `lab.LoadScript`、`lab.CheckScript`、`lab.ProjectSymbols` 保留转发入口，加载令牌和超时控制只在子包中维护。

搭建器、共用因子条件和统计结果在父包使用类型别名，字段与 JSON 格式保持一致。搭建器校验通过 `Validate(mode)` 访问，共用因子条件通过 `Validate`、`Build`、`Describe` 访问；旧的私有方法不作为跨包接口。

| 工作内容 | 主要入口 |
|---|---|
| HTTP 路由、静态资源与服务装配 | [server.go](../../internal/lab/server.go) |
| 任务互斥、停止、状态与报告 | [runner.go](../../internal/lab/runner.go)、[reports.go](../../internal/lab/reports.go) |
| 账户回测与多变体加载 | [account_backtest.go](../../internal/lab/account_backtest.go)、[researchrun/runner.go](../../internal/researchrun/runner.go) |
| 自由搭建器目录、规则与模板 | [builder/catalog.go](../../internal/lab/builder/catalog.go)、[builder/spec.go](../../internal/lab/builder/spec.go)、[builder/filter.go](../../internal/lab/builder/filter.go)、[builder/template.go](../../internal/lab/builder/template.go) |
| 搭建器存储与 HTTP 接口 | [builder_store.go](../../internal/lab/builder_store.go)、[builder_server.go](../../internal/lab/builder_server.go) |
| 因子分析、观察样本与评价标签 | [analysis.go](../../internal/lab/analysis.go)、[factor_analysis_v4.go](../../internal/lab/factor_analysis_v4.go)、[factor_observation.go](../../internal/lab/factor_observation.go)、[factor_label.go](../../internal/lab/factor_label.go) |
| 因子统计诊断 | [factorstats/ic.go](../../internal/lab/factorstats/ic.go)、[factorstats/turnover.go](../../internal/lab/factorstats/turnover.go)、[factorstats/neutralize.go](../../internal/lab/factorstats/neutralize.go) |
| 候选、研究协议与验证 | [factor_candidate.go](../../internal/lab/factor_candidate.go)、[research_protocol.go](../../internal/lab/research_protocol.go)、[factor_validation.go](../../internal/lab/factor_validation.go) |
| 组合模型、实验执行与接口 | [factor_model.go](../../internal/lab/factor_model.go)、[portfolio_experiment_store.go](../../internal/lab/portfolio_experiment_store.go)、[portfolio_runner.go](../../internal/lab/portfolio_runner.go)、[portfolio_handlers.go](../../internal/lab/portfolio_handlers.go) |
| 组合计算与交易会计 | [portfolioresearch/](../../internal/portfolioresearch/) |
| 兼容脚本执行与符号注册 | [script/interp.go](../../internal/lab/script/interp.go)、[script/symbols.go](../../internal/lab/script/symbols.go)；[原入口转发](../../internal/lab/interp.go) |
| 行情更新与调度 | [data_update.go](../../internal/lab/data_update.go) |
| 前端页面、组件和样式 | [index.html](../../internal/lab/web/lab/index.html)、[app.js](../../internal/lab/web/lab/js/app.js)、[lab.css](../../internal/lab/web/lab/css/lab.css) |

脚本加载、超时、初始化安全和 Yaegi 绑定测试放在子包；HTTP 生命周期测试留在父包。两者通过编译期嵌入共用 [strategy.go](../../internal/lab/script/testdata/strategy.go) 夹具，不依赖测试时工作目录。运行 `go test ./internal/lab/...` 覆盖两层测试。

搭建器工厂覆盖测试、统计诊断手算与边界测试随实现归入子包；存储、候选接入、完整回测与报告快照测试保留在父包。条件基线继续逐前缀对比已有哈希，目录整理不更新其预期值。

候选、实验、验证存储与执行编排共享研究协议、证据与状态机，继续由父包装配；组合算法已在 `internal/portfolioresearch/`，多变体数据执行已在 `internal/researchrun/`。本轮按这些现有职责完成归属，不为目录分类增加存储接口或任务回调。

## 运行目录与维护边界

- `data/` 保存行情与 Lab 状态，`output/` 保存报告，`bin/` 保存二进制；三者均被 Git 忽略。文档整理不移动真实数据。
- [deploy.sh](../../deploy.sh) 挂载 `data/`、`output/`、`config/` 和 `strategies/script/`；[Dockerfile](../../Dockerfile) 从 `cmd/lab` 构建服务。调整这些路径需要单独处理配置与存量数据兼容。
- Lab 的 `web/lab/` 由 `go:embed` 嵌入，前端测试位于 `internal/lab/web/*.mjs`；保持资源和测试的相对位置。
- `AGENTS.md`、`PROJECT_RULES.md`、`MEMORY.md`、`CONTEXT.md`、`DESIGN.md`、`UX-CONTRACT.md` 留在根目录，作为协作、术语与设计的固定入口。
- `cmd/backtest/main.go` 是作者的调参草稿入口，保留现有内容和路径。
- 设计与计划保留文档标注日期和历史状态；旧记录不能代替当前源码与测试结果。发布候选的历史 hash 不因目录迁移而重新冻结。

## 目录改造验收（2026-10-17）

本轮完成 34 份文档的分类迁移、统一导航、三个 Lab 子包的职责拆分，以及调用方、测试、代码注释和文档链接的同步。其余源码沿用现有包边界，运行数据、资源嵌入和部署挂载路径保持原位。

| 验证 | 结果 |
|---|---|
| `go test ./... -count=1`（Linux / Go 1.25，上海时区，禁网 Docker） | 全部通过，含发布门禁与新子包 |
| `go build ./...` | 全部命令及包构建通过 |
| `go vet ./internal/lab/...` | 通过 |
| `node --test internal/lab/web/*.mjs` | 67 项通过 |
| `python3 tests/deploy_test.py` | 5 项通过，使用 Docker 替身 |
| 迁移等价性检查 | 实现仅调整归属及标识符可见性；统计公式、字段与原数值断言一致；391 个原有 Lab 测试函数保留 |
| 本地文档链接与导航检查 | 文件、跨文件章节引用有效，导航覆盖全部文档 |

目录改造验收阶段未执行部署、真实数据回测、浏览器验收或竞态检测；后续部署冒烟结果见下节。运行规则沿用原有行为，包括旧脚本执行超时后需重启服务恢复加载。

## 部署冒烟验证（2026-10-17）

用户授权后通过 `bash ./deploy.sh` 构建并部署当前工作区，2026-10-17 22:02（上海时间）启动新容器。镜像 ID 为 `sha256:4488cc0ec694d42cb7661692b7c377d33d8a7e3865cb96160b1f3b7b33669427`。监听地址保持 `0.0.0.0:8765`，配置、行情、报告和脚本的原挂载及读写权限保持一致。

- 新容器运行正常，重启次数为 0，未发现 OOM 或 panic/fatal 记录。
- 组件目录为 73 个可用组件和 34 个未接入条目，因子目录为 46 项。
- 2 个已保存策略及其详情、16 份历史回测列表及最近报告详情、3 个候选均可读取；模型、组合实验、组合验证、行情与任务状态接口返回正常。
- 兼容脚本 API 的合法与非法编译校验符合预期；首页及本地入口资源返回成功。
- 浏览器验证策略加载、运行配置弹窗打开并取消、因子目录、组合研究空态、历史回测与收益图正常，未发现控制台错误。
- 保留停止状态的旧容器 `strategy-lab-previous-20261017T220202-63280` 供恢复，未清理旧容器或数据。

本次冒烟未启动新的真实数据回测或行情更新，未实际触发故障回滚；不替代此前的全量自动化测试或竞态检测。
