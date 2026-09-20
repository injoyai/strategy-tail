# 候选因子保存与复用设计

> 日期：2026-09-17  
> 状态：已实施并验证（2026-09-17；`go test ./...` 全绿 + 浏览器验收；`-race` 因 Windows 工具链限制未运行）  
> 范围：Strategy Lab 因子分析结果版本化、候选因子方案保存、证据追溯与简单策略复用

## 1. 背景

当前 Strategy Lab 已具备完整的单因子研究链路：

- `strategies/factor/registry.go` 提供固定因子目录与 `Build(kind, days)`；
- 因子研究可输出逐日 Rank IC、分组收益、年度稳定性和覆盖率；
- 简单策略可用 `FactorFilterSpec` 把一个注册因子转换成固定区间过滤条件；
- 分析结果自动写入 `output/factor/<kind>/report.json`、`ic.csv` 和 `report.html`。

但“分析结果看起来不错”以后仍缺少可复用闭环：

1. 同一 `kind` 再次分析会覆盖固定路径下的上次报告，无法追溯选择依据；
2. 页面没有“保存为候选因子”或“我的候选因子”入口；
3. 因子目录没有实现版本，代码变化后旧结论可能被新实现静默复用；
4. 分析配置、证据和策略过滤条件彼此分离，用户只能手工重填；
5. 一次样本内表现不能直接证明因子可交易，系统不应自动把它标成“好因子”。

本设计补齐以下闭环：

```text
因子分析 → 保存不可变证据 → 形成候选方案 → 配置固定区间 → 加入简单策略 → 对照回测
```

## 2. 目标与非目标

### 2.1 目标

1. 每次因子分析获得稳定、唯一的 `analysisId`，历史报告不再互相覆盖。
2. 用户可把当前分析保存为“候选因子方案”，并保留完整分析证据快照。
3. 候选方案明确绑定因子 `kind`、参数、实现版本和分析口径。
4. 已配置固定区间的候选方案可以一键加入现有简单策略表单。
5. 因子实现版本不一致时 fail closed，不允许静默复用。
6. 保存、更新和归档均使用追加修订，保留历史，不物理删除研究证据。
7. 旧分析报告和旧简单策略仍可读取、运行；只有新候选保存走严格版本合同。

### 2.2 首版不做

- 不根据 IC、t 值或分组收益自动判定“优秀”“可交易”或“验证通过”；
- 不把每日因子值矩阵持久化；仍按现有规则运行时计算；
- 不从每日动态分位组的累计最小值/最大值自动推导固定阈值；
- 不保存或应用 TopN 方案；现有简单策略 API 尚未定义 Universe、调仓频率和排名执行口径；
- 不做多因子标准化、加权打分、自动寻参或自动组合；
- 不把任意 Yaegi/Go 代码序列化成因子；自定义算法仍须实现 `core.Factor` 并注册；
- 不提供物理删除。错误或弃用的记录只能归档；
- 不把本地候选库描述成跨设备同步、云端备份或团队协作系统。

## 3. 核心概念与边界

### 3.1 四个不同对象

| 对象 | 回答的问题 | 当前/新增 |
|---|---|---|
| 因子定义 `Factor` | 这个数值如何计算？ | 当前已有，Go 实现 |
| 分析报告 `AnalysisReport` | 它在某个样本、时期和未来收益窗口表现如何？ | 当前已有，需版本化 |
| 候选因子方案 `FactorCandidate` | 哪份证据值得保留，准备怎样使用？ | 本次新增 |
| 策略过滤 `FactorFilterSpec` | 回测当天按什么固定区间决定是否保留信号？ | 当前已有，需绑定版本 |

保存候选方案不会复制因子算法，也不会自动产生买入信号。它保存的是：

```text
注册因子引用 + 使用意图 + 不可变分析证据 + 审计元数据
```

### 3.2 为什么叫“候选”

一次分析通常参与了因子、参数、方向和阈值的选择，属于样本内研究证据。即使 IC、分组收益和年度结果较好，也可能来自偶然、样本选择或多重试验。

因此首版状态只有：

- `candidate`：已保存，尚未经过独立样本外验证；
- `archived`：不再用于新策略，但历史证据保留。

`validated` 状态暂不开放。后续只有在系统能绑定冻结参数、非重叠验证区间和独立回测报告时，才可增加机器可验证的晋级流程。

## 4. 关键设计决策

### 4.1 因子实现必须显式版本化

在 `strategies/factor/registry.go` 的内部条目及 `CatalogEntry` 增加：

```go
ImplementationVersion int `json:"implementationVersion"`
```

规则：

1. 现有因子从版本 `1` 开始；
2. 任何会改变相同输入下数值输出的算法、缺失值或窗口语义变更都必须递增；
3. 仅修改标题、描述和示例无需递增；
4. 候选方案保存当时版本；当前版本不一致时标记 `stale`，禁止直接加入策略；
5. 首版不保留旧算法实现，因此版本不一致代表“必须重新分析”，不是自动迁移。

`FactorFilterSpec` 增加可选字段：

```go
FactorVersion int `json:"factorVersion,omitempty"`
```

- `0`：旧配置兼容模式，按当前注册版本运行；
- `>0`：严格模式，必须等于当前注册版本，否则校验失败；
- 从候选库加入策略时必须写入正版本，不允许降级为 `0`。

### 4.2 分析报告使用不可变 ID 和版本目录

`AnalysisReport` 增加：

```go
AnalysisID string `json:"analysisId"`
```

并将 `AnalysisVersion` 从 `2` 升为 `3`。`FactorSnapshot` 同时记录 `ImplementationVersion`。

新主存储路径：

```text
output/factor/<kind>/<analysisId>/report.json
output/factor/<kind>/<analysisId>/ic.csv
output/factor/<kind>/<analysisId>/report.html
output/factor/<kind>/latest.json
```

其中 `latest.json` 只保存当前指针：

```json
{
  "analysisId": "an_20260917T153012123Z_a1b2c3d4",
  "kind": "momentum",
  "report": "an_20260917T153012123Z_a1b2c3d4/report.json"
}
```

兼容期继续镜像写入当前固定的 `report.json`、`ic.csv`、`report.html`，旧页面和人工路径不立即失效。不可变版本报告与 `latest.json` 写入是主结果；CSV、HTML 和兼容镜像仍可 best-effort。

ID 由服务端生成，格式使用 UTC 时间加 `crypto/rand` 随机后缀。客户端不得提供文件路径，所有 ID 在用于路径前必须经过严格正则校验。

### 4.3 候选记录与证据均采用追加修订

本地存储路径：

```text
data/lab/factor-candidates/<candidateId>/
  revisions/000001.json
  revisions/000001.analysis.json
  revisions/000002.json
  revisions/000002.analysis.json
```

说明：

- `data/` 是本地持久数据，不进入 Git；
- 每次创建、编辑或归档都写一个新修订，不覆盖旧修订；
- 每个修订绑定一份完整 `AnalysisReport` JSON 快照；
- `*.analysis.json` 先写，`*.json` 后写；候选记录文件是该修订的提交标记；
- 每个文件均在同目录先写临时文件再原子 rename；扫描时忽略临时文件；
- 候选记录保存证据 SHA-256，读取时校验；不匹配则报错，不静默继续；
- 候选数量预计很小，首版通过目录扫描读取最新修订，不维护容易双写失真的全局索引。

修改候选名称、备注或固定区间时，沿用同一 `candidateId` 并增加 `revision`。改变 `kind`、`days` 或分析证据则创建新的候选，不在原记录上偷换研究对象。

### 4.4 分位分析不自动变成固定阈值

当前等频分组的每个交易日边界不同；报告中的组 `factorMin/factorMax` 是跨日累计分布，甚至相邻组范围可能重叠。因此：

- 分位分析可用于判断方向和观察稳定性；
- 不得用 Q1/QN 的累计边界自动生成 `gte/lte/between`；
- 用户可以先以 `observe` 模式只保存证据；
- 只有用户明确填写固定阈值，或分析本身使用固定 `bins`，才保存 `range` 使用配置；
- 即使来源是固定 `bins`，保存界面仍展示最终原始数值并要求用户确认。

## 5. 数据合同

### 5.1 候选记录

```go
type CandidateStatus string

const (
    CandidateStatusCandidate CandidateStatus = "candidate"
    CandidateStatusArchived  CandidateStatus = "archived"
)

type FactorRef struct {
    Kind                  string `json:"kind"`
    Days                  int    `json:"days"`
    Name                  string `json:"name"`
    Unit                  string `json:"unit"`
    ImplementationVersion int    `json:"implementationVersion"`
}

type CandidateUse struct {
    Mode   string            `json:"mode"` // observe | range
    Filter *FactorFilterSpec `json:"filter,omitempty"`
}

type CandidateEvidence struct {
    AnalysisID      string                 `json:"analysisId"`
    AnalysisVersion int                    `json:"analysisVersion"`
    ReportSHA256    string                 `json:"reportSha256"`
    Window          int                    `json:"window"`
    Range           AnalysisRange          `json:"range"`
    Grouping        GroupingConfig          `json:"grouping"`
    Stats           ICStats                 `json:"stats"`
    Summary         QuintileSummary         `json:"summary"`
    Coverage        researchrun.Coverage    `json:"coverage"`
    YearCoverage    researchrun.YearCoverage `json:"yearCoverage"`
    FirstDataDate   string                 `json:"firstDataDate"`
    LastDataDate    string                 `json:"lastDataDate"`
    FinishedAt      string                 `json:"finishedAt"`
}

type FactorCandidate struct {
    SchemaVersion int               `json:"schemaVersion"`
    ID            string            `json:"id"`
    Revision      int               `json:"revision"`
    Name          string            `json:"name"`
    Status        CandidateStatus   `json:"status"`
    Factor        FactorRef         `json:"factor"`
    Use           CandidateUse      `json:"use"`
    Evidence      CandidateEvidence `json:"evidence"`
    Notes         string            `json:"notes,omitempty"`
    CreatedAt     string            `json:"createdAt"`
    UpdatedAt     string            `json:"updatedAt"`
    CreateRequestID   string        `json:"createRequestId"`
    CreateRequestHash string        `json:"createRequestHash"`
}
```

`CreateRequestID` 由前端在一次保存动作开始时生成并在重试中复用；
`CreateRequestHash` 是服务端对规范化 `analysisId + name + use + notes` 计算的
SHA-256。同一 request ID 和相同 hash 返回已存在记录；同一 request ID 携带不同
内容返回 409，避免网络重试产生副本，也避免幂等键被错误复用。

### 5.2 派生兼容状态

API 返回时增加不落盘的派生字段：

```json
{
  "compatibility": {
    "state": "ready",
    "currentVersion": 1,
    "reason": ""
  }
}
```

状态：

- `ready`：因子存在且当前实现版本与候选一致；
- `stale`：因子存在但实现版本已变化；
- `missing`：注册表已无此 kind。

只有 `status=candidate`、`use.mode=range` 且 `compatibility.state=ready` 时可以“加入策略”。

### 5.3 校验规则

- 名称 trim 后 1～80 个 Unicode 字符；备注最多 2000 个字符；
- `requestId` 必须是合法 UUID；一次用户保存动作的所有重试必须复用同一值；
- `observe` 不得携带 Filter；
- `range` 必须携带 Filter，且其 kind/days/version 必须与 FactorRef 完全一致；
- Filter 继续使用 `gte/lte/between`，阈值必须有限，`between.min <= max`；
- Candidate 的因子和证据字段全部由服务端从分析报告推导，客户端不能覆写；
- 更新必须携带 `expectedRevision`；不匹配返回 `409 Conflict`；
- `archived` 记录默认不出现在活动列表，但仍可读取和恢复；
- 不接受客户端传入存储路径、SHA-256、创建时间或修订号。

## 6. API 设计

### 6.1 分析历史

| 方法 | 路径 | 行为 |
|---|---|---|
| `GET` | `/api/analyses?kind=<kind>` | 返回历史分析摘要，时间倒序 |
| `GET` | `/api/analysis/{analysisId}` | 返回指定不可变报告 |
| `GET` | `/api/analysis/latest` | 保持现有合同；重启后可从 `latest.json` 恢复 |

旧 v2 报告仍可展示；由于缺少不可变 ID 和因子实现版本，不能直接保存为候选。页面提示“请按当前版本重新运行分析”。

### 6.2 候选库

#### 创建

`POST /api/factor-candidates`

```json
{
  "requestId": "e5f8ce78-914d-4e17-96bb-9269d4fef7d7",
  "analysisId": "an_20260917T153012123Z_a1b2c3d4",
  "name": "20日动量高值候选",
  "use": {
    "mode": "range",
    "filter": {
      "kind": "momentum",
      "days": 20,
      "factorVersion": 1,
      "operator": "gte",
      "min": 0.05
    }
  },
  "notes": "先作为候选，后续单独做样本外验证"
}
```

服务端按 `analysisId` 读取不可变报告、校验版本并复制证据。首次创建返回 `201`；
相同 request ID、相同内容的重试返回原记录和 `200`；相同 request ID、不同内容
返回 `409`。

#### 查询和修订

| 方法 | 路径 | 行为 |
|---|---|---|
| `GET` | `/api/factor-candidates?includeArchived=false` | 列出每个 ID 的最新修订 |
| `GET` | `/api/factor-candidates/{id}` | 返回最新修订和完整兼容状态 |
| `PUT` | `/api/factor-candidates/{id}` | 用 `expectedRevision` 追加名称、备注、Use 或状态修订 |

`PUT` 不允许修改 FactorRef 或 Evidence。归档和恢复也是状态修订，不提供 `DELETE`。

### 6.3 错误码

| 状态码 | 场景 |
|---|---|
| `400` | JSON、名称、使用配置、ID 格式无效 |
| `404` | 分析或候选不存在 |
| `409` | 修订冲突、分析不是 v3、因子版本已漂移 |
| `413` | 请求体超过 64 KiB |
| `500` | 本地存储损坏、证据哈希不匹配或原子写失败 |

错误响应沿用 `{ "error": "..." }`，不得向前端暴露任意本机绝对路径。

## 7. 前端工作流

### 7.1 分析结果保存

在因子研究结果区增加“保存为候选”按钮。点击后展开内联表单，不新增前端框架：

1. 候选名称；
2. 使用方式：
   - 仅保存观察结果；
   - 配置固定区间后保存；
3. `gte/lte/between` 与原始阈值输入；
4. 备注；
5. 明确提示“保存为候选不代表样本外验证通过”。

等频分析不得自动填充累计分组值域。固定 bins 分析可把对应断点作为建议值展示，但提交前仍需确认。

保存按钮在请求期间保持尺寸、禁用重复提交并通过 `aria-live` 报告结果。若页面上的 `analysisId` 已被新分析替换，服务端拒绝旧请求，页面保留用户输入并提示刷新证据。

一次保存动作开始时用 `crypto.randomUUID()` 生成 `requestId`；网络失败后的重试复用
该值，用户修改表单并开始新的保存动作时生成新值。不得在每次 HTTP 重试时重新生成。

### 7.2 我的候选因子

因子研究 Tab 增加紧凑列表，字段包括：

- 名称、因子实例名、保存用途；
- 证据区间、未来收益窗口、完成时间；
- 候选/已归档状态；
- ready/stale/missing 兼容状态；
- 查看证据、配置/编辑区间、加入策略、归档/恢复。

默认只显示活动候选，可切换查看归档项。列表失败时不影响新分析，显示独立重试入口。

### 7.3 加入简单策略

“加入策略”只做表单转换，不自动运行回测：

```text
Candidate.Use.Filter → StrategySpec.factorFilters[]
```

规则：

- 只支持 `range + ready + candidate`；
- 写入 `factorVersion`；
- 相同 `kind + days` 已存在时聚焦原条件并询问用户是否用候选值替换，不静默重复；
- 当前已有 4 个条件时提示先删除一个；
- 加入后切换到“策略与运行”，聚焦新增条件并标记“尚未回测”；
- 百分比只在显示层乘 100，API 和存储始终使用原始比例。

## 8. 一致性、恢复与安全

### 8.1 原子性与并发

- `CandidateStore` 使用独立互斥锁串行化创建和修订；
- 修订采用 `expectedRevision` 防止两个浏览器页面互相覆盖；
- 分析报告目录一经发布不可修改；
- `latest.json` 只是指针，损坏不会破坏历史目录；可通过扫描恢复，但首版不自动重写；
- 服务器启动时不清理残留临时文件，读取时忽略，避免误删用户数据。

### 8.2 路径安全

- 目录名只来自服务端生成且正则验证的 ID；
- 路径拼接后用 `filepath.Rel` 再确认目标仍在配置根目录内；
- API 不接收文件名、相对路径或绝对路径；
- 错误日志可记录内部路径，HTTP 响应只返回安全描述。

### 8.3 可复现边界

候选保存可证明“当时用哪个注册版本、什么数据区间和分析口径得到这份结果”，但不能单独证明：

- 原始行情数据库后来未被修订；
- 外部 TDX 数据具备严格 PIT 版本；
- 候选在未来或样本外仍有效；
- 当前日线收盘成交口径可实盘复现。

页面和报告继续披露这些边界，不使用“生产可用”“无前视”“已验证”等承诺性措辞。

## 9. 兼容与迁移

1. v2 与更早分析报告继续展示，但不能直接保存为候选；重新运行后生成 v3。
2. 保留固定 `output/factor/<kind>/report.json` 等兼容镜像至少一个版本周期。
3. 旧 `StrategySpec` 没有 `factorVersion`，反序列化为 `0` 并继续按当前行为运行。
4. 候选库加入策略时必须写正版本；后续版本漂移由后端拒绝。
5. 不自动扫描旧 `output/factor/*/report.json` 建候选，避免把无法证明身份与版本的报告伪造成可信证据。
6. `data/lab/factor-candidates` 为本地状态；首版交付说明必须提示用户备份该目录。

## 10. 验收标准

### AC-01 分析历史不覆盖

同一因子连续运行两次，得到不同 `analysisId` 和两个可读取的版本目录；`latest` 指向第二次，第一次仍可读取。

### AC-02 候选证据不可变

保存候选后再次分析同一 kind，候选绑定的报告内容与 SHA-256 不变。

### AC-03 创建幂等

相同 request ID 和内容的创建重试不会产生第二个 candidate ID；幂等键被不同内容复用时返回 409。

### AC-04 修订不覆盖

修改名称或阈值产生 revision 2，revision 1 文件保留；错误的 `expectedRevision` 返回 409。

### AC-05 版本漂移 fail closed

候选版本与当前 registry 版本不同后，列表显示 stale，“加入策略”禁用，服务端也拒绝复用。

### AC-06 等频边界不被误用

等频分析保存表单不自动将累计组值域填入固定阈值；用户可只保存观察证据。

### AC-07 一键加入策略

ready 的 range 候选生成完全一致的 `FactorFilterSpec`，含正 `factorVersion`；不会自动启动回测。

### AC-08 重启可恢复

服务重启后，分析历史、最新报告和候选列表均可从磁盘恢复，不依赖进程内 atomic 指针。

### AC-09 旧合同兼容

旧分析报告仍可查看；未含 `factorVersion` 的旧策略请求仍按当前行为校验和运行。

### AC-10 损坏 fail closed

候选证据哈希不一致或 JSON 损坏时返回明确错误，不跳过坏记录后宣称列表完整。

## 11. 后续阶段

样本外验证成为系统能力后，可在不改变首版含义的前提下新增：

```text
candidate → validation_pending → validated / rejected → archived
```

晋级必须绑定冻结的候选 revision、非重叠验证区间、数据版本、完整策略回测报告和明确验收门槛；不能由用户手工勾选“已验证”。
