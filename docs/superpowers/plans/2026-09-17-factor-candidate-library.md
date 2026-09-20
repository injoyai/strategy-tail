# 候选因子保存与复用实施计划

> 日期：2026-09-17  
> 状态：已实施并验证（2026-09-17，Task 1～7 完成；`-race` 因 Windows 工具链限制未运行，见 MEMORY.md）  
> 设计依据：`docs/superpowers/specs/2026-09-17-factor-candidate-library-design.md`  
> 目标：交付分析历史版本化、候选库、证据追溯和简单策略复用；不实现自动评级、TopN 保存或样本外认证

## 1. 实施原则

1. 先建立报告身份和因子实现版本，再开放保存入口，避免先写入无法追溯的数据。
2. 所有新存储组件必须可注入临时根目录，测试不得读写真实 `data/` 或 `output/`。
3. 候选库只引用注册因子，不序列化 `core.Factor` 接口实例。
4. 新候选严格绑定因子版本；旧策略以 `factorVersion=0` 保持兼容。
5. 任何更新都追加修订，不覆盖、不删除旧证据。
6. 文档实施期间不修改 `AGENTS.md`，不触碰当前未提交的 `internal/lab/strategy_spec*` 与 `server_test.go` 改动之外的用户工作；实施时若需编辑这些共享文件，先以当前工作树为基线融合。

## 2. 交付顺序与依赖

```text
Task 1 因子实现版本
  ↓
Task 2 分析 ID 与历史存储
  ↓
Task 3 候选领域模型与追加式 Store
  ↓
Task 4 候选 API
  ↓
Task 5 前端保存、列表与加入策略
  ↓
Task 6 端到端、恢复与兼容验证
  ↓
Task 7 文档状态与项目记忆
```

Task 1～4 是后端可信性闭环，不能跳过直接做按钮。Task 5 不得在后端版本校验完成前启用“加入策略”。

---

## Task 1：增加因子实现版本并贯穿分析与策略配置

**Files**

- Modify: `strategies/factor/registry.go`
- Modify: `strategies/factor/registry_test.go`
- Modify: `internal/lab/analysis.go`
- Modify: `internal/lab/analysis_run_test.go`
- Modify: `internal/lab/strategy_spec.go`
- Modify: `internal/lab/strategy_spec_test.go`

### Step 1：扩展目录合同

为 `CatalogEntry` 和内部 `entry` 增加 `ImplementationVersion`。现有 14 个注册条目显式填写 `1`，禁止依赖零值。

测试新增：

- `TestRegistryImplementationVersions`：所有版本大于 0；
- `All()` 与 `Catalog()` 返回相同版本；
- 同 kind 不允许重复；现有顺序合同保持不变。

### Step 2：扩展分析快照

`FactorSnapshot` 增加 `ImplementationVersion`，构建 `AnalysisReport` 时从 `f.Catalog(kind)` 复制。将 `AnalysisVersion` 升为 `3`。

更新测试，明确断言：

```text
analysisVersion == 3
factor.implementationVersion == registry 当前版本
```

旧 JSON 缺字段时仍可反序列化为 0，仅用于展示。

### Step 3：扩展简单策略过滤合同

`FactorFilterSpec` 增加：

```go
FactorVersion int `json:"factorVersion,omitempty"`
```

校验规则：

- `0`：旧请求兼容；
- `<0`：400；
- `>0`：必须等于 `Catalog(kind).ImplementationVersion`，否则返回可读错误；
- `build()` 前重复执行版本校验或只允许从已校验路径进入，不能绕过。

现有测试请求默认 `0`，确保无回归；新增当前版本通过、未来版本和旧版本拒绝用例。

### Step 4：验证

```powershell
gofmt -w strategies/factor/registry.go strategies/factor/registry_test.go internal/lab/analysis.go internal/lab/analysis_run_test.go internal/lab/strategy_spec.go internal/lab/strategy_spec_test.go
go test ./strategies/factor ./internal/lab
```

**完成条件**：注册表、分析报告和策略过滤三处使用同一个显式版本；候选复用所需的 fail-closed 基础成立。

---

## Task 2：分析报告 ID、不可变历史与重启恢复

**Files**

- Create: `internal/lab/analysis_store.go`
- Create: `internal/lab/analysis_store_test.go`
- Modify: `internal/lab/analysis.go`
- Modify: `internal/lab/runner.go`
- Modify: `internal/lab/server.go`
- Modify: `internal/lab/server_test.go`

### Step 1：实现 ID 生成与校验

提供包内函数：

```go
func newAnalysisID(now time.Time, random io.Reader) (string, error)
func validAnalysisID(id string) bool
```

格式固定为：

```text
an_<UTC yyyyMMddTHHmmssSSSZ>_<8 hex>
```

测试固定时钟和随机 reader，断言格式、唯一后缀、非法路径字符被拒绝。不得使用全局 `math/rand`。

### Step 2：抽出 AnalysisStore

建议接口：

```go
type AnalysisStore struct {
    root string
    mu   sync.Mutex
}

func NewAnalysisStore(root string) *AnalysisStore
func (s *AnalysisStore) Save(rep *AnalysisReport) error
func (s *AnalysisStore) Get(id string) (*AnalysisReport, error)
func (s *AnalysisStore) Latest(kind string) (*AnalysisReport, error)
func (s *AnalysisStore) List(kind string) ([]AnalysisSummary, error)
```

生产默认 root 为 `output/factor`；测试使用 `t.TempDir()`。

写入顺序：

1. 创建 `<kind>/<analysisId>/`；
2. 原子写主 `report.json`；
3. best-effort 写 `ic.csv` 和 `report.html`；
4. 原子更新 `<kind>/latest.json`；
5. best-effort 写兼容镜像 `<kind>/report.json|ic.csv|report.html`。

主报告或 latest 指针失败则本次分析返回错误；兼容镜像失败只记录日志，不破坏已发布历史。

所有由 ID 派生的路径在使用前执行根目录约束检查。`List` 只扫描合法目录和有效报告，遇到损坏的正式报告返回错误；`.tmp` 文件忽略。

### Step 3：Runner 生成并传递 AnalysisID

在 `StartAnalysis` 获取互斥锁后生成 ID，并传给 `runAnalysis`。ID 生成失败时释放锁并返回，不启动 goroutine。

调整签名示意：

```go
func (r *Runner) runAnalysis(id string, cfg AnalyzeConfig, stop chan struct{}) (*AnalysisReport, error)
```

`AnalysisReport.AnalysisID` 在导出前完成赋值。不要让客户端通过 `AnalyzeConfig` 注入 ID。

`Runner` 或 `Server` 持有可注入的 `AnalysisStore`；测试构造函数不得写生产目录。

### Step 4：扩展查询 API

新增：

- `GET /api/analyses?kind=momentum`
- `GET /api/analysis/{analysisId}`

增强现有 `/api/analysis/latest`：内存无最近报告时，可按请求 kind 或全局最近指针从磁盘恢复。若保留无参数合同，应增加一个全局 latest 指针；若不增加，全局 latest 的恢复行为必须在 API 文档中明确，不能随机扫描后猜测。

推荐在 root 增加全局 `latest.json`，内容同时含 kind 与 analysisId；写入顺序晚于 kind latest。这样现有无参数 `/latest` 可在重启后确定恢复。

### Step 5：测试

新增或更新：

- `TestAnalysisStoreKeepsHistory`：同 kind 两次保存均可读取；
- `TestAnalysisStoreLatest`：kind 与全局 latest 正确；
- `TestAnalysisStoreIgnoresTemp`；
- `TestAnalysisStoreRejectsTraversalID`；
- `TestServerAnalysisHistoryAPI`；
- `TestServerLatestAnalysisAfterRestart`；
- 现有因子分析 E2E 更新为 v3 路径。

运行：

```powershell
gofmt -w internal/lab
go test ./internal/lab -run 'Analysis(Store|History|Latest|API)'
```

**完成条件**：AC-01、AC-08 的分析部分通过；同 kind 报告不再覆盖，旧固定路径仍存在。

---

## Task 3：候选领域模型与追加式本地 Store

**Files**

- Create: `internal/lab/factor_candidate.go`
- Create: `internal/lab/factor_candidate_test.go`
- Create: `internal/lab/factor_candidate_store.go`
- Create: `internal/lab/factor_candidate_store_test.go`

### Step 1：实现领域类型和纯校验

按设计文档 §5 定义：

- `CandidateStatus`
- `FactorRef`
- `CandidateUse`
- `CandidateEvidence`
- `FactorCandidate`
- `CandidateCompatibility`
- API 请求类型 `CreateCandidateRequest`、`UpdateCandidateRequest`

纯函数：

```go
func normalizeCreateCandidateRequest(req CreateCandidateRequest) (CreateCandidateRequest, error)
func candidateFromAnalysis(req CreateCandidateRequest, rep *AnalysisReport, now time.Time) (FactorCandidate, error)
func compatibilityOf(c FactorCandidate) CandidateCompatibility
func candidateRequestHash(req CreateCandidateRequest) (string, error)
```

创建时 FactorRef/Evidence 只能由报告推导。`range` 模式复用 `FactorFilterSpec.validate()`，并额外断言 kind/days/version 与报告快照完全一致。

### Step 2：实现 Store

建议接口：

```go
type CandidateStore struct {
    root string
    mu   sync.Mutex
    now  func() time.Time
    rand io.Reader
}

func NewCandidateStore(root string) *CandidateStore
func (s *CandidateStore) Create(req CreateCandidateRequest, rep *AnalysisReport) (FactorCandidate, bool, error)
func (s *CandidateStore) Get(id string) (FactorCandidate, error)
func (s *CandidateStore) List(includeArchived bool) ([]FactorCandidate, error)
func (s *CandidateStore) Update(id string, req UpdateCandidateRequest) (FactorCandidate, error)
```

`Create` 的 bool 表示是否新建。同一 `requestId` 与相同请求 hash 返回已有记录；
同一 `requestId` 与不同 hash 返回冲突。不同 request ID 可从同一分析创建不同名称或
用途的多个候选。

`Update`：

- 必须匹配 `expectedRevision`；
- 只允许名称、备注、Use、Status 变化；
- FactorRef、Evidence、CreatedAt、CreateRequestID、CreateRequestHash 保持；
- 追加 revision+1；
- `candidate → archived → candidate` 合法；其他状态拒绝。

### Step 3：实现证据快照与哈希

每个修订写入完整、规范 JSON 的 `NNNNNN.analysis.json`，再计算 SHA-256 并写候选记录。更新名称或状态仍复制证据到新修订，保证单个修订目录自足，不依赖旧文件。

读取时：

1. 找到最大合法 revision；
2. 读取候选记录；
3. 读取对应证据；
4. 校验 SHA-256；
5. 校验 CandidateEvidence 摘要与完整报告关键字段一致。

禁止遇到损坏后退回上一 revision 冒充最新；应返回损坏错误，让用户知道最新写入不可信。

### Step 4：测试

至少覆盖：

- `TestCandidateFromAnalysisObserve`
- `TestCandidateFromAnalysisRange`
- `TestCandidateRejectsQuantileAutoBoundary`（纯合同：请求没有用户阈值时不能生成 range）
- `TestCandidateStoreCreateIdempotent`
- `TestCandidateStoreRejectsIdempotencyKeyReuse`
- `TestCandidateStoreCreatesDistinctUses`
- `TestCandidateStoreAppendsRevision`
- `TestCandidateStoreRevisionConflict`
- `TestCandidateStoreArchiveRestore`
- `TestCandidateStoreEvidenceHashMismatch`
- `TestCandidateStoreRejectsTraversalID`
- `TestCandidateStoreRestartRecovery`

运行：

```powershell
gofmt -w internal/lab/factor_candidate*.go
go test ./internal/lab -run 'Candidate'
```

**完成条件**：AC-02、AC-03、AC-04、AC-10 在 Store 层通过。

---

## Task 4：候选库 HTTP API

**Files**

- Modify: `internal/lab/server.go`
- Modify: `internal/lab/server_test.go`
- Modify: `internal/lab/runner.go`（仅在依赖注入需要时）

### Step 1：依赖注入

`Server` 增加 `analysisStore` 与 `candidateStore`。保留 `NewServer()` 作为生产默认构造；新增仅供测试的构造函数，例如：

```go
func newServerWithStores(analyses *AnalysisStore, candidates *CandidateStore) *Server
```

避免测试通过 `os.Chdir` 共享全局目录。

### Step 2：注册路由

新增：

```text
POST /api/factor-candidates
GET  /api/factor-candidates
GET  /api/factor-candidates/{id}
PUT  /api/factor-candidates/{id}
```

创建流程：

1. `http.MaxBytesReader(..., 64<<10)`；
2. 解码并拒绝多余 JSON；
3. 校验 requestId 与 analysisId；
4. 从 `AnalysisStore` 读取 v3 报告；
5. 检查报告因子当前版本仍一致；
6. 调 `CandidateStore.Create`；
7. 新建返回 201，幂等命中返回 200。

更新流程使用 `expectedRevision`；Store 的冲突错误映射 409。HTTP 错误体不包含本机绝对路径。

### Step 3：列表返回兼容状态

列表和详情响应为候选记录加 `compatibility`，由当前 registry 实时计算，不回写历史 JSON。

排序：活动候选在前，同状态按 `updatedAt` 倒序，再按 ID 稳定排序。`includeArchived` 只接受 `true/false`，非法值返回 400。

### Step 4：API 测试

覆盖：

- 创建 observe/range；
- 相同 request ID 与内容幂等；相同 request ID、不同内容冲突；
- 同一 analysis 不同用途可创建多条；
- v2 报告拒绝；
- 未知 analysis 404；
- 过大 body 413；
- 多余字段 400；
- list 默认隐藏 archived；
- get、update、revision conflict；
- 因子版本漂移 409 / compatibility stale；
- Store 损坏 500 且错误体不泄露绝对路径。

运行：

```powershell
gofmt -w internal/lab/server.go internal/lab/server_test.go
go test ./internal/lab -run 'Server.*(Candidate|AnalysisHistory)'
```

**完成条件**：API 行为与设计 §6 一致，且服务重启后仍能查询。

---

## Task 5：前端保存、候选列表和加入策略

**Files**

- Modify: `internal/lab/web/lab/index.html`
- Modify: `DESIGN.md`（仅补候选库的控件所有权与状态，不重写视觉基线）

### Step 1：分析结果增加保存入口

在结果摘要与“添加到策略条件”附近加入：

- `保存为候选` 按钮；
- 内联保存表单；
- 名称、用途、区间操作符/阈值、备注；
- 样本内候选提示；
- 保存成功/失败 `aria-live` 区域。

仅当报告 `analysisVersion>=3`、存在 `analysisId` 和正 `implementationVersion` 时启用。旧报告显示重新分析提示。

等频模式默认选择“仅保存观察结果”，且阈值为空；禁止读取 `groups[].factorMin/factorMax` 自动填入。bins 模式可以展示断点建议，但必须由用户选择和确认。

一次保存动作开始时用 `crypto.randomUUID()` 生成 `requestId`；同一次网络重试复用，
用户编辑后重新发起保存时生成新值。保存成功或用户主动取消后清除当前 request ID。

### Step 2：候选列表

在因子研究 Tab 增加“我的候选因子”区块：

- 初始加载、空态、加载失败/重试；
- 活动/归档切换；
- 名称、因子、用途、证据范围、状态、兼容状态；
- 查看证据、编辑、加入策略、归档/恢复。

`stale` 和 `missing` 使用琥珀提示并给出文字，不用颜色独立传意。列表使用现有紧凑表格与移动端滚动容器。

### Step 3：编辑与乐观并发

编辑表单携带当前 `revision` 作为 `expectedRevision`。409 时保留输入，重新加载服务端记录并提示用户比较后重试，不自动覆盖。

### Step 4：加入策略

把 Candidate 的 Filter 映射进现有 `factorRows`/`buildSpec()` 数据结构，并写入 `factorVersion`。

前端需要同步调整：

- 因子条件 DOM 保存版本值；
- `buildSpec()` 提交 `factorVersion`；
- 普通手工新建条件从当前 `/api/factors` 条目取版本；
- 已有 kind+days 时不新增，聚焦原项并要求明确替换；
- 4 条上限不变；
- 加入后切换到策略 Tab、聚焦条件并显示“尚未回测”。

不要在加入后自动调用 `/api/strategy/run`。

### Step 5：前端静态与构建检查

提取内联脚本运行语法检查（沿用仓库现有方法），并编译 embed：

```powershell
node --check <提取后的临时 js 文件>
go build ./cmd/lab
```

临时文件放系统临时目录，不落仓库。

### Step 6：浏览器验收

至少验证：

1. 等频分析后打开保存表单，阈值没有被自动填入；
2. 仅观察模式保存成功并出现在活动列表；
3. range 模式保存原始比例正确（页面 5% → JSON 0.05）；
4. 刷新页面、重启服务后候选仍存在；
5. range+ready 候选可加入策略且包含 factorVersion；
6. observe、stale、missing、archived 均不能加入；
7. 重复 kind+days 不会静默增加；
8. 4 条上限提示正确；
9. 归档默认隐藏、切换后可见并可恢复；
10. 900px/560px 无页面级横向溢出，键盘焦点可见，console 无错误。

**完成条件**：AC-05、AC-06、AC-07 的 UI 与服务端双层约束均通过。

---

## Task 6：端到端、恢复与兼容验证

**Files**

- Create: `internal/lab/factor_candidate_e2e_test.go`
- Modify as needed: `internal/lab/analysis_run_test.go`
- Modify as needed: `internal/lab/factor_e2e_test.go`

### Step 1：完整链路测试

在 `t.TempDir()` 下构造日 K 数据与两个独立 Store，执行：

```text
运行 momentum 分析
→ 得到 v3 analysisId
→ POST 保存 range 候选
→ GET 列表
→ 转成 FactorFilterSpec
→ POST /api/strategy/run
→ 报告 strategySpec 中保留 kind/days/factorVersion/阈值
```

测试只验证合同和小样本行为，不用全市场数据。

### Step 2：历史与重启测试

连续运行同 kind 两次，保存第一次为候选，然后重建 `Server`：

- 两份分析均可按 ID 读取；
- latest 指向第二份；
- 候选仍绑定第一份且哈希一致；
- 候选列表可恢复；
- 固定兼容镜像指向第二份。

### Step 3：旧合同测试

- 读取不含 analysisId/version 的 v2 fixture：可展示、不可保存；
- 发送不含 factorVersion 的旧 StrategySpec：仍运行；
- 发送候选生成的严格 StrategySpec：版本相同运行，版本漂移拒绝。

### Step 4：损坏与边界测试

- 修改证据一个字节 → 候选读取失败；
- 制造残留 `.tmp` → 正常忽略；
- 非法 ID、`..`、斜杠、绝对路径 → 400；
- 并发两个相同 expectedRevision 更新 → 只允许一个成功；
- 精确重复创建 → 同一个 ID。

### Step 5：全量验证

PowerShell 下使用独立可写缓存：

```powershell
$env:GOCACHE = Join-Path $env:TEMP 'strategy-tail-gocache'
gofmt -l strategies/factor internal/lab
go test ./strategies/factor ./internal/researchrun ./internal/lab
go test ./...
go vet ./...
go build ./...
git diff --check
```

本机已知 `go test -race` 可能以 `0xc0000139` 启动失败；如仍失败，记录为 Windows 工具链限制，不宣称 race 通过，并在可用的 Linux CI 环境补跑。

### Step 6：最终差异检查

确认：

- 未修改受保护的 `core`/`cmd/screen` 结构；
- 未修改 `AGENTS.md`；
- 未覆盖用户已有的 `strategy_spec` 相关未提交改动；
- 无真实 `data/lab/factor-candidates`、`output/factor`、临时 JS、exe 等产物进入 Git；
- 新 JSON 字段均有兼容测试；
- HTTP 错误不泄露绝对路径。

**完成条件**：AC-01～AC-10 全部具备自动化或明确浏览器证据。

---

## Task 7：交付文档与项目记忆

**Files**

- Modify: `docs/superpowers/specs/2026-09-17-factor-candidate-library-design.md`
- Modify: `docs/superpowers/plans/2026-09-17-factor-candidate-library.md`
- Modify: `DESIGN.md`
- Modify: `MEMORY.md`

仅在实现和验证完成后执行：

1. 设计文档状态改为“已实施并验证”，记录日期；
2. 实施计划勾选实际完成项，未完成项保持明确；
3. `DESIGN.md` 记录候选保存/列表/兼容状态的 canonical owner；
4. `MEMORY.md` 只记录长期合同：存储路径、版本 fail-closed、候选状态和分位边界不得自动转阈值；
5. 交付说明分别列出自动化测试、浏览器验证、未验证项和剩余风险。

不得在尚未实现时提前把设计或记忆写成“已完成”。

---

## 3. API 验收样例

### 3.1 保存观察候选

请求：

```json
{
  "requestId": "e5f8ce78-914d-4e17-96bb-9269d4fef7d7",
  "analysisId": "an_20260917T153012123Z_a1b2c3d4",
  "name": "20日动量观察候选",
  "use": {"mode": "observe"},
  "notes": "等待独立年份验证"
}
```

响应关键字段：

```json
{
  "created": true,
  "candidate": {
    "schemaVersion": 1,
    "id": "fc_20260917T153114456Z_b2c3d4e5",
    "revision": 1,
    "status": "candidate",
    "factor": {
      "kind": "momentum",
      "days": 20,
      "implementationVersion": 1
    },
    "use": {"mode": "observe"},
    "evidence": {
      "analysisId": "an_20260917T153012123Z_a1b2c3d4",
      "analysisVersion": 3
    },
    "compatibility": {"state": "ready", "currentVersion": 1}
  }
}
```

### 3.2 保存可复用区间

```json
{
  "requestId": "7dd474c2-a6e9-40af-af01-dc3c46be6dad",
  "analysisId": "an_20260917T153012123Z_a1b2c3d4",
  "name": "20日动量至少5%",
  "use": {
    "mode": "range",
    "filter": {
      "kind": "momentum",
      "days": 20,
      "factorVersion": 1,
      "operator": "gte",
      "min": 0.05
    }
  }
}
```

加入策略后必须得到同值：

```json
{
  "kind": "momentum",
  "days": 20,
  "factorVersion": 1,
  "operator": "gte",
  "min": 0.05
}
```

### 3.3 修订冲突

```json
{
  "expectedRevision": 1,
  "name": "新名称",
  "use": {"mode": "observe"},
  "status": "candidate"
}
```

若服务端已是 revision 2，返回：

```json
{
  "error": "候选因子已被更新，请重新加载后再保存"
}
```

HTTP 状态为 `409 Conflict`，不得自动覆盖。

## 4. 需求追踪矩阵

| 设计验收 | 实施任务 | 主要测试 |
|---|---|---|
| AC-01 分析历史不覆盖 | Task 2 | `TestAnalysisStoreKeepsHistory` |
| AC-02 候选证据不可变 | Task 3/6 | `TestCandidateStoreRestartRecovery` + E2E |
| AC-03 创建幂等 | Task 3/4 | `TestCandidateStoreCreateIdempotent` |
| AC-04 修订不覆盖 | Task 3/4 | `TestCandidateStoreAppendsRevision`、冲突测试 |
| AC-05 版本漂移 fail closed | Task 1/4/5 | registry/spec/API/UI 测试 |
| AC-06 分位边界不误用 | Task 3/5 | 纯校验 + 浏览器验收 |
| AC-07 一键加入策略 | Task 1/5/6 | 候选到策略 E2E |
| AC-08 重启恢复 | Task 2/3/6 | restart tests |
| AC-09 旧合同兼容 | Task 1/2/6 | v2/legacy fixtures |
| AC-10 损坏 fail closed | Task 3/4/6 | hash/corruption tests |

## 5. 明确留待后续的事项

- 候选的独立样本外验证任务与 `validated/rejected` 状态；
- 候选导出/导入、跨机器同步和备份 UI；
- TopN/分位排名候选的 Universe、调仓与组合构建合同；
- 因子值缓存、数据库和数据版本哈希；
- 多因子正交化、标准化、加权与组合优化；
- 任意自定义脚本因子的安全持久化。

这些事项不得用首版字段占位后宣称已有能力。
