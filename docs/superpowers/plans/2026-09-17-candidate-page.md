# 候选因子独立页面（第 5 个 Tab）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
> 状态：已实施并验证（2026-09-17）

**Goal:** 把候选因子管理（列表卡 + 证据/编辑 dialog）从"因子研究"tab 拆为独立第 5 个 tab（`?tab=candidates`），保存表单留在分析页并增加"查看候选 →"跳转。

**Architecture:** 纯前端 DOM 迁移。新增 tab5 panel，`#candidateSection` 卡整体迁入，元素 ID 不变使候选 JS 模块零修改；两个候选 `<dialog>` 迁到 main 顶层（tab panel 外）——否则 tab4 hidden 后 `showModal()` 不渲染。HTTP API 与 Go 侧零改动。

**Tech Stack:** Go embed 静态页（`internal/lab/web/lab/index.html` 单文件，ES6 + `<dialog>`）。

**Spec:** `docs/superpowers/specs/2026-09-17-candidate-page-design.md`

**工作流约定:** 本仓库由用户决定 git 提交，计划不含 commit 步骤；每个任务以验证收尾。

**行号基准:** 以下行号基于当前 `index.html`（写入本计划时）。执行时以代码锚点（注释、ID、函数名）为准，行号仅作定位辅助。

---

### Task 1: tab 导航扩展（按钮 + slug 映射）

**Files:**
- Modify: `internal/lab/web/lab/index.html`（tab 按钮区 ~245-249；`TAB_SLUG`/`TAB_NUM` ~686-687）

- [ ] **Step 1.1: 新增第 5 个 tab 按钮**

在 `#tabbtn3`（交易明细）按钮之后、`</div>`（tablist 结束）之前插入：

```html
  <button type="button" role="tab" id="tabbtn5" data-tab="5" aria-controls="tab5" aria-selected="false" tabindex="-1" onclick="switchTab(5)"><span class="tab-index">05</span><span class="tab-copy"><span class="tab-title">候选因子</span><span class="tab-desc">证据 · 修订 · 复用</span></span></button>
```

- [ ] **Step 1.2: 扩展 slug 映射**

```js
const TAB_SLUG = {1: 'strategy', 2: 'compare', 3: 'trades', 4: 'factor', 5: 'candidates'};
const TAB_NUM = {strategy: 1, compare: 2, trades: 3, factor: 4, candidates: 5};
```

- [ ] **Step 1.3: 语法检查**

在仓库根运行：

```
node -e "const fs=require('fs');const s=fs.readFileSync('internal/lab/web/lab/index.html','utf8');const m=[...s.matchAll(/<script>([\s\S]*?)<\/script>/g)];if(!m.length)throw new Error('no script');m.forEach((x,i)=>{new Function(x[1]);console.log('script '+i+' ok')})"
```

预期输出：`script 0 ok`（如有多个 script 块则全部 ok）。

---

### Task 2: 候选区与 dialog 迁移

**Files:**
- Modify: `internal/lab/web/lab/index.html`（候选卡 ~559-577；两个 dialog ~579-625；tab4 panel 结束 `</div>` ~626；`</main>` ~627）

**关键约束（设计修正点）:** `#candEditDlg` 与 `#candEvDlg` 当前在 tab4 panel **内部**。tab4 被 `hidden`（`display:none`）后，其内 `showModal()` 无法渲染，因此两个 dialog 必须迁到 main 顶层（所有 tab panel 之外）。

- [ ] **Step 2.1: 从 tab4 panel 剪出候选卡与两个 dialog**

删除 tab4 panel 内的整段（从 `<!-- 我的候选因子 -->` 注释起，到证据 dialog 的 `</dialog>` 止，即原 559-625 行）。

- [ ] **Step 2.2: 在 tab4 panel 结束后、`</main>` 前插入 tab5 panel 与两个 dialog**

```html
<div class="tab-panel" id="tab5" role="tabpanel" aria-labelledby="tabbtn5" tabindex="0" hidden>
  <!-- 我的候选因子 -->
  <div class="card" id="candidateSection" style="margin-top:18px">
    <h3>我的候选因子</h3>
    <div class="cand-toolbar">
      <button class="secondary" id="btnCandArchived" onclick="toggleCandidatesArchived()">查看归档</button>
      <button class="secondary" id="btnCandRefresh" onclick="loadCandidates()">刷新</button>
      <span class="hint" style="margin:0" id="candListHint">候选保存的是不可变分析证据；保存不代表样本外验证通过。</span>
    </div>
    <div class="cand-table-wrap" id="candTableWrap" style="display:none">
      <table id="candTable">
        <thead><tr>
          <th>名称</th><th>因子</th><th>用途</th><th>证据区间</th><th>状态</th><th>兼容</th><th>操作</th>
        </tr></thead>
        <tbody id="candBody"></tbody>
      </table>
    </div>
    <div class="empty" id="candEmpty" role="status">暂无候选因子：完成分析后可“保存为候选”。</div>
    <div class="warnbar" id="candLoadErr" style="display:none"></div>
  </div>
</div>

  <!-- 候选编辑对话框（乐观并发：携带 expectedRevision，409 不自动覆盖） -->
  <dialog id="candEditDlg" aria-labelledby="candEditTitle">
    <div class="dlg-head">
      <h3 id="candEditTitle">编辑候选</h3>
      <button class="dlg-close" onclick="$('candEditDlg').close()" aria-label="关闭">×</button>
    </div>
    <div class="cand-form">
      <div class="cf-row"><label for="candEditName">名称</label>
        <input type="text" id="candEditName" maxlength="80">
      </div>
      <div class="cf-row"><label for="candEditUseMode">使用方式</label>
        <select id="candEditUseMode">
          <option value="observe">仅保存观察结果</option>
          <option value="range">配置固定区间后保存</option>
        </select>
      </div>
      <div class="cf-row" id="candEditRangeRow" style="display:none">
        <label for="candEditRangeOp">区间条件</label>
        <select id="candEditRangeOp" style="width:104px">
          <option value="gte">≥ 至少</option>
          <option value="lte">≤ 至多</option>
          <option value="between">介于</option>
        </select>
        <input type="number" id="candEditRangeMin" step="any" placeholder="下限" aria-label="区间下限">
        <input type="number" id="candEditRangeMax" step="any" placeholder="上限" aria-label="区间上限">
      </div>
      <div class="cf-row"><label for="candEditNotes">备注</label>
        <textarea id="candEditNotes" maxlength="2000"></textarea>
      </div>
      <div class="cf-row">
        <div class="cf-actions">
          <button class="primary" id="btnCandEditSave" onclick="saveCandidateEdit()">保存修订</button>
          <button class="secondary" onclick="$('candEditDlg').close()">取消</button>
          <span class="hint" style="margin:0" id="candEditHint"></span>
        </div>
      </div>
      <div class="msg" id="candEditMsg" role="status" aria-live="polite"></div>
    </div>
  </dialog>

  <!-- 证据查看对话框 -->
  <dialog id="candEvDlg" aria-labelledby="candEvTitle">
    <div class="dlg-head">
      <h3 id="candEvTitle">候选证据</h3>
      <button class="dlg-close" onclick="$('candEvDlg').close()" aria-label="关闭">×</button>
    </div>
    <div id="candEvBody" style="font-size:12px;line-height:1.9;color:var(--ink2)"></div>
    <div class="hint" style="margin-top:10px">证据是样本内研究记录，不可变保存；不代表样本外验证通过。</div>
  </dialog>
</main>
```

> 注意：上面对话框内部字段以迁移时刻的实际 DOM 为准（元素 ID 一一对应 `openCandidateEdit`/`saveCandidateEdit`/`viewCandidateEvidence` 的引用：`candEditName`、`candEditUseMode`、`candEditRangeRow`、`candEditRangeOp`、`candEditRangeMin`、`candEditRangeMax`、`candEditNotes`、`btnCandEditSave`、`candEditHint`、`candEditMsg`、`candEvBody`）。执行时先读取当前实际代码整段移动，禁止凭上面骨架重写——以移动代替重写，保证属性/类名逐字节不变。

- [ ] **Step 2.3: 语法检查**

同 Task 1 Step 1.3 命令，预期全部 `ok`。

- [ ] **Step 2.4: 确认无重复 ID**

```
node -e "const fs=require('fs');const s=fs.readFileSync('internal/lab/web/lab/index.html','utf8');const ids=[...s.matchAll(/id=\"([^\"]+)\"/g)].map(m=>m[1]);const dup=ids.filter((v,i)=>ids.indexOf(v)!==i);console.log(dup.length?['DUP',...new Set(dup)].join(' '):'no dup')"
```

预期输出：`no dup`。

---

### Task 3: "查看候选 →"跳转

**Files:**
- Modify: `internal/lab/web/lab/index.html`（`#candidateMsg` ~534；`saveCandidate` 成功分支 ~2028；`renderCandidateSaveState` ~1899）

- [ ] **Step 3.1: 表单内新增跳转按钮**

在 `#candidateMsg` 所在行之后（`#candidateForm` 内部末尾）插入：

```html
          <button type="button" class="secondary" id="btnViewCandidates" onclick="switchTab(5)" hidden>查看候选 →</button>
```

- [ ] **Step 3.2: 保存成功后显示**

`saveCandidate()` 成功分支（`setCandidateMsg(d.created ? ... , true);` 一行之后、`$('candidateForm').style.display = 'none';` 之前）插入：

```js
    $('btnViewCandidates').hidden = false;
```

- [ ] **Step 3.3: 新分析渲染时重置隐藏**

`renderCandidateSaveState()` 函数体开头（`const rep = currentAnalysis;` 之后）插入：

```js
  const vb = $('btnViewCandidates');
  if (vb) vb.hidden = true;
```

- [ ] **Step 3.4: 语法检查**

同 Task 1 Step 1.3 命令，预期全部 `ok`。

---

### Task 4: 构建与回归验证

- [ ] **Step 4.1: 构建与全量测试**

```
gofmt -l . ; go build ./... ; go test ./...
```

预期：`gofmt -l` 无输出（Go 文件本次未改，应保持干净）；`go build` 成功；`go test ./...` 全部 PASS（HTML 内容变化不影响既有测试，若失败需排查 embed 或意外改动）。

- [ ] **Step 4.2: 差异检查**

```
git diff --check ; git status
```

预期：`git diff --check` 无输出（exit 0）；`git status` 仅显示本任务相关文件（`index.html`）。

---

### Task 5: 浏览器验收（spec 清单）

启动方式同前次验收：`go run ./cmd/lab`（默认 `:8765`，被占用时自动切换端口，以启动日志为准），浏览器打开实际地址。

- [ ] **Step 5.1: 直达候选 tab**

打开 `http://127.0.0.1:<port>/?tab=candidates`。预期：直接显示"我的候选因子"列表（有历史候选则渲染行），无控制台错误；地址栏为 `?tab=candidates`。

- [ ] **Step 5.2: 默认 tab 不变**

打开 `http://127.0.0.1:<port>/`（无参数）。预期：落在"因子研究"tab；页面底部不再有候选列表卡；`#candidateForm` 仍在分析结果区内。

- [ ] **Step 5.3: 保存后跳转链接**

在因子研究页运行一次因子分析 → 打开保存表单 → 保存一个候选。预期：成功消息出现，且下方出现"查看候选 →"按钮；点击后切到候选 tab，列表含新候选。

- [ ] **Step 5.4: 加入策略跨 tab**

在候选 tab 点某 range 候选的"加入策略"。预期：切到策略 tab，条件卡带"尚未回测"chip，重复 kind+days 弹确认。

- [ ] **Step 5.5: dialog 在候选 tab 可用**

在候选 tab 点击"证据"与"编辑"。预期：两个 dialog 均正常弹出（验证 Step 2 迁移修复了 hidden 祖先问题）；"归档/恢复/查看归档"切换正常。

- [ ] **Step 5.6: 键盘循环**

聚焦 tab 栏按 `ArrowRight`/`ArrowLeft`/`End`。预期：可循环到 `05 候选因子` 按钮并切换。

- [ ] **Step 5.7: 控制台无错误**

浏览器 console 无新增 JS 错误。

---

### Task 6: 文档同步

**Files:**
- Modify: `DESIGN.md`（§4.1 候选因子库 UI 合同）

- [ ] **Step 6.1: 更新候选列表归属描述**

在 §4.1 的"候选列表"条目处，补充独立 tab 归属与 dialog 位置约束：

```markdown
- 候选管理自 2026-09-17 起位于独立"候选因子"tab（`?tab=candidates`，第 5 个 tab）：列表卡（“我的候选因子”）与两个候选 dialog 均不在因子研究页内；dialog 位于 main 顶层（tab panel 之外），不得移回任何 panel——hidden 祖先（display:none）内 showModal 不渲染。保存表单留在因子研究页分析结果区，保存成功后出现“查看候选 →”跳转（新分析渲染时重置隐藏）。
```

- [ ] **Step 6.2: 确认 spec 状态**

将 `docs/superpowers/specs/2026-09-17-candidate-page-design.md` 头部 `> 状态：设计已确认，待实施` 改为 `> 状态：已实施并验证（2026-09-17）`。本计划文档头部补充一行 `> 状态：已实施并验证（2026-09-17）`。

---

## Self-Review 记录

- **Spec 覆盖**：spec §2.1 → Task 1；§2.2 → Task 2（含 dialog 位置修正）；§2.3 → Task 2（JS 零修改）+ Task 3（跳转）；§2.4 → Task 2（`#candLoadErr` 随卡迁移）；§3 测试与验收 → Task 4/5；文档 → Task 6。无遗漏。
- **占位符**：Task 2 Step 2.2 的 dialog 骨架附带了"以移动代替重写"的强制约束与完整 ID 清单，执行者必须整段搬移现有代码。
- **类型一致性**：`switchTab(5)`、`TAB_SLUG[5]='candidates'`、`btnViewCandidates` 在各任务间一致。
