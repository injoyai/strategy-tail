// 根组件：masthead / Tabs（WAI-ARIA + URL）/ 批量任务状态条 / 6 个功能面板 + 顶层对话框。
// 从 index.html 迁移：switchTab/键盘导航/renderTaskProgress/showPendingTask/pollStatus/
// pollStatusOnce/stopBatchTask/dismissTaskDock/initMotion。
// 任务状态放入 store.taskDock（响应式），状态条模板渲染；done 分流钩子：
// analysis→01 载入最新分析；portfolio→06 pfOnTaskDone；其余→03 载入最新报告。
import { createApp, watch } from './vendor/vue.esm-browser.prod.js';
import { state, runtime } from './store.js';
import { Api } from './api.js';
import { TABS, TAB_SLUG, TAB_NUM, TASK_LABEL, PF_PHASE } from './constants.js';
import { animatePanel, animateStatus, initMotion } from './motion.js';
import Tab1Run from './components/Tab1Run.js';
import Tab2Compare from './components/Tab2Compare.js';
import Tab3Trades from './components/Tab3Trades.js';
import Tab4Factor from './components/Tab4Factor.js';
import Tab5Candidates from './components/Tab5Candidates.js';
import Tab6Portfolio from './components/Tab6Portfolio.js';
import CandidateSaveDlg from './components/CandidateSaveDlg.js';
import CandidateEditDlg from './components/CandidateEditDlg.js';
import CandidateEvidenceDlg from './components/CandidateEvidenceDlg.js';
import PfExperimentDlg from './components/PfExperimentDlg.js';
import PfValidationDlg from './components/PfValidationDlg.js';

const App = {
  name: 'App',
  components: {
    Tab1Run, Tab2Compare, Tab3Trades, Tab4Factor, Tab5Candidates, Tab6Portfolio,
    CandidateSaveDlg, CandidateEditDlg, CandidateEvidenceDlg, PfExperimentDlg, PfValidationDlg,
  },
  data() {
    return {state, tabs: TABS};
  },
  provide() {
    return {
      switchTab: n => this.switchTab(n),
      startTask: (task, opts) => this.startTask(task, opts),
      openCandidateDlg: () => this.$refs.candSave && this.$refs.candSave.openDlg(),
      openCandidateEdit: c => this.$refs.candEdit && this.$refs.candEdit.openDlg(c),
      openCandidateEvidence: c => this.$refs.candEv && this.$refs.candEv.openDlg(c),
      openPfExpDlg: m => this.$refs.pfExpDlg && this.$refs.pfExpDlg.openDlg(m),
      openPfValDlg: m => this.$refs.pfValDlg && this.$refs.pfValDlg.openDlg(m),
      getPfGates: () => this.$refs.tab6 && this.$refs.tab6.getPfGates(),
    };
  },
  mounted() {
    // 刷新只触发一次轮询；任务运行中由 pollStatusOnce 续上轮询
    this.pollStatusOnce();
    // 任务卡首次出现时做揭示动画
    watch(() => state.taskDock, (v, old) => {
      if (v && !old) this.$nextTick(() => animateStatus(this.$refs.statusbar));
    });
    initMotion();
    // Tab 状态从 URL 恢复；无参数时优先进入因子研究，但不自动重跑任务
    const initial = TAB_NUM[new URLSearchParams(location.search).get('tab')];
    if (initial && initial !== 4) this.switchTab(initial);
  },
  methods: {
    // ============ Tabs ============
    setPanelRef(el, n) {
      if (el) this.$refs['panel' + n] = el; else delete this.$refs['panel' + n];
    },
    switchTab(n) {
      state.activeTab = n;
      if (n === 6) {
        const p = new URLSearchParams(location.search);
        p.set('tab', 'portfolio'); // 06 保留并更新扩展参数（model/revision/run/val/filter/sort/page）
        history.replaceState(null, '', '?' + p.toString());
      } else if (n === 4) {
        history.replaceState(null, '', location.pathname);
      } else {
        history.replaceState(null, '', '?tab=' + TAB_SLUG[n]);
      }
      this.$nextTick(() => animatePanel(this.$refs['panel' + n]));
    },
    onTabsKeydown(e) {
      const tabs = this.tabs;
      const i = tabs.findIndex(t => t.n === state.activeTab);
      let target = null;
      if (e.key === 'ArrowRight') target = tabs[(i + 1) % tabs.length];
      else if (e.key === 'ArrowLeft') target = tabs[(i - 1 + tabs.length) % tabs.length];
      else if (e.key === 'Home') target = tabs[0];
      else if (e.key === 'End') target = tabs[tabs.length - 1];
      if (target) {
        e.preventDefault();
        this.switchTab(target.n);
        document.getElementById('tabbtn' + target.n)?.focus();
      }
    },

    // ============ 批量任务卡 ============
    // 分析与回测共用唯一的批量任务组件；总量未就绪时保持不定进度，避免伪造百分比。
    renderTaskProgress(s) {
      const st = s.state || 'idle';
      if (st === 'idle') { state.taskDock = null; return; }
      const taskName = TASK_LABEL[s.task] || '批量任务';
      const isPortfolio = s.task === 'portfolio' || s.task === 'portfolio_validation';
      let done = Number(s.doneCodes) || 0;
      let total = Number(s.totalCodes) || 0;
      if (isPortfolio) {
        done = Number(s.done) || 0;
        total = Number(s.total) || 0;
        if (Number(s.done) === -1 || Number(s.total) === -1) { done = 0; total = 0; }
      }
      const rawProgress = Number(s.progress);
      const progress = Number.isFinite(rawProgress) ? Math.max(0, Math.min(100, rawProgress)) : 0;
      const connectionError = st === 'connection-error';
      const determinate = st !== 'running' || (total > 0 && done > 0);
      const title = {
        running: `${taskName}进行中`,
        done: `${taskName}已完成`,
        error: `${taskName}失败`,
        stopped: `${taskName}已停止`,
        'connection-error': '进度读取中断',
      }[st] || taskName;
      const detail = isPortfolio
        ? (total > 0 && (done > 0 || st !== 'running')
          ? `${done}/${total} 单元`
          : PF_PHASE[s.phase] || '正在准备组合任务')
        : total > 0 && (done > 0 || st !== 'running')
          ? `${done}/${total} 只股票`
          : '正在准备股票列表';
      const current = s.currentCode && done > 0
        ? `当前 ${s.currentCode}`
        : isPortfolio && st === 'running'
          ? (PF_PHASE[s.phase] || '组合任务进行中')
          : st === 'done' ? '结果已就绪'
          : st === 'stopped' ? '已保留当前表单配置'
          : connectionError ? '任务可能仍在后台运行'
          : '加载任务范围与数据';
      state.taskDock = {
        state: connectionError ? 'error' : st,
        title,
        percent: determinate ? `${Math.round(progress)}%` : '准备中',
        value: determinate ? (st === 'done' ? 100 : progress) : null,
        indeterminate: !determinate,
        detail,
        current,
        error: s.error || '',
        running: st === 'running',
        connectionError,
      };
    },
    showPendingTask(task, opts) {
      runtime.lastTask = task;
      runtime.prevRunning = true;
      state.taskRunning = true;
      this.renderTaskProgress(Object.assign({state: 'running', task, doneCodes: 0, totalCodes: 0, progress: 0}, opts || {}));
    },
    startTask(task, opts) {
      this.showPendingTask(task, opts);
      this.pollStatus();
    },
    dismissTaskDock() {
      state.taskDock = null;
    },
    async stopBatchTask() {
      state.stopBusy = true;
      if (state.taskDock) state.taskDock.current = '正在停止任务…';
      try {
        await Api.stop();
      } catch (e) {
        if (state.taskDock) state.taskDock.error = `停止失败：${e.message}`;
      } finally {
        state.stopBusy = false;
      }
    },

    // ============ 状态轮询 ============
    pollStatus() {
      if (runtime.pollTimer) clearInterval(runtime.pollTimer);
      this.pollStatusOnce();
      runtime.pollTimer = setInterval(() => this.pollStatusOnce(), 2000);
    },
    async pollStatusOnce() {
      try {
        const s = await Api.status();
        const running = s.state === 'running';
        if (s.state !== 'idle') runtime.lastTask = s.task || runtime.lastTask;
        const visibleStatus = s.state === 'idle' && runtime.prevRunning
          ? {...s, state: 'stopped', task: runtime.lastTask}
          : s;
        this.renderTaskProgress(visibleStatus);
        state.taskRunning = running;
        if (running && !runtime.pollTimer) runtime.pollTimer = setInterval(() => this.pollStatusOnce(), 2000);
        if (s.state === 'done') {
          clearInterval(runtime.pollTimer); runtime.pollTimer = null;
          if (s.task === 'analysis') {
            state.currentAnalysis = await Api.latestAnalysis();
            state.factorMsg = {text: '分析完成', ok: true};
            this.switchTab(4);
          } else if (s.task === 'portfolio' || s.task === 'portfolio_validation') {
            // 组合任务完成：终态以持久 Store 为准，刷新 06 页对应详情（不自动切页）。
            if (s.runId && this.$refs.tab6) await this.$refs.tab6.pfOnTaskDone(s.runId, s.task);
          } else {
            state.currentReport = await Api.latestReport();
            this.switchTab(2);
            // 根据 source 恢复正确的结果说明，不把高级多变体脚本误当成多条件组合
            if (state.currentReport.source === 'simple' && state.currentReport.comparison) {
              const c = state.currentReport.comparison;
              state.scriptMsg = {text: `多条件组合对照已生成：基准 ${c.baselineTrades} 笔 → 组合 ${c.combinedTrades} 笔` +
                (c.retentionRate == null ? '' : `（保留 ${(c.retentionRate * 100).toFixed(1)}%）`) +
                '，详见“组合对比”摘要。', ok: true};
            } else {
              state.scriptMsg = {text: '已载入高级脚本多变体报告。', ok: true};
            }
          }
        } else if (s.state === 'idle' && runtime.prevRunning && runtime.lastTask === 'analysis') {
          state.factorMsg = {text: '任务已停止', ok: false};
          clearInterval(runtime.pollTimer); runtime.pollTimer = null;
        } else if (s.state === 'error' && runtime.lastTask === 'analysis') {
          state.factorMsg = {text: s.error || '分析失败', ok: false};
          clearInterval(runtime.pollTimer); runtime.pollTimer = null;
        } else if (s.state === 'idle' || s.state === 'error') {
          clearInterval(runtime.pollTimer); runtime.pollTimer = null;
        }
        runtime.prevRunning = running;
      } catch (e) {
        clearInterval(runtime.pollTimer); runtime.pollTimer = null;
        state.taskRunning = false;
        this.renderTaskProgress({
          state: 'connection-error', task: runtime.lastTask, progress: 0,
          error: `无法读取任务进度：${e.message}。请重试；后台任务状态尚未确认。`,
        });
        runtime.prevRunning = false;
      }
    },
  },
  template: `
<header class="masthead">
  <div class="brand-mark" aria-hidden="true">
    <svg viewBox="0 0 32 32" fill="none"><path d="M5 24.5V17l6-6 5 5 10-10" stroke="#ecd69c" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"/><path d="M20 6h6v6" stroke="#7aa8ff" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"/><path d="M5 27h22" stroke="#ecd69c" stroke-opacity=".5" stroke-width="1.4" stroke-linecap="round"/></svg>
  </div>
  <div class="brand-copy">
    <span class="eyebrow">A-SHARE RESEARCH WORKBENCH</span>
    <h1>策略实验室</h1>
  </div>
  <div class="header-meta" aria-label="运行环境">
    <span class="local-badge"><span class="live-dot" aria-hidden="true"></span>本地研究会话</span>
  </div>
</header>

<div class="tabs" role="tablist" aria-label="功能区" @keydown="onTabsKeydown">
  <button v-for="t in tabs" :key="t.n" type="button" role="tab" :id="'tabbtn' + t.n" :data-tab="t.n"
    :aria-controls="'tab' + t.n" :aria-selected="String(state.activeTab === t.n)"
    :tabindex="state.activeTab === t.n ? 0 : -1" @click="switchTab(t.n)">
    <span class="tab-index">{{ t.index }}</span>
    <span class="tab-copy"><span class="tab-title">{{ t.title }}</span><span class="tab-desc">{{ t.desc }}</span></span>
  </button>
</div>

<main>
  <section class="statusbar" ref="statusbar" v-show="!!state.taskDock" :data-state="state.taskDock ? state.taskDock.state : 'idle'"
    aria-live="polite" aria-atomic="true" aria-labelledby="statusState">
    <div class="status-head">
      <div class="status-title-wrap">
        <span class="status-dot" aria-hidden="true"></span>
        <span class="status-kicker">BATCH JOB</span>
        <span id="statusState">{{ state.taskDock ? state.taskDock.title : '' }}</span>
      </div>
      <span id="statusPercent">{{ state.taskDock ? state.taskDock.percent : '' }}</span>
    </div>
    <div class="progress-outer">
      <progress class="progress-inner" :class="{indeterminate: state.taskDock && state.taskDock.indeterminate}"
        :max="100" :value="state.taskDock ? state.taskDock.value : 0" aria-label="批量任务进度">{{ state.taskDock ? state.taskDock.percent : '' }}</progress>
    </div>
    <div class="status-foot">
      <div class="status-meta">
        <span id="statusDetail">{{ state.taskDock ? state.taskDock.detail : '' }}</span>
        <span id="statusCurrent">{{ state.taskDock ? state.taskDock.current : '' }}</span>
      </div>
      <div class="status-actions">
        <button type="button" class="secondary" v-if="state.taskDock && state.taskDock.connectionError" @click="pollStatus()">重试读取</button>
        <button type="button" class="danger" v-if="state.taskDock && state.taskDock.running" @click="stopBatchTask" :disabled="state.stopBusy" :aria-busy="String(state.stopBusy)">停止任务</button>
        <button type="button" class="secondary" v-if="state.taskDock && !state.taskDock.running" @click="dismissTaskDock()">关闭</button>
      </div>
    </div>
    <div class="msg" v-if="state.taskDock && state.taskDock.error" role="alert">{{ state.taskDock.error }}</div>
  </section>

  <div v-for="t in tabs" :key="t.n" class="tab-panel" :id="'tab' + t.n" :ref="el => setPanelRef(el, t.n)"
    role="tabpanel" :aria-labelledby="'tabbtn' + t.n" tabindex="0" v-show="state.activeTab === t.n">
    <tab1-run v-if="t.n === 1" />
    <tab2-compare v-if="t.n === 2" />
    <tab3-trades v-if="t.n === 3" />
    <tab4-factor v-if="t.n === 4" />
    <tab5-candidates v-if="t.n === 5" />
    <tab6-portfolio ref="tab6" v-if="t.n === 6" />
  </div>
</main>

<candidate-save-dlg ref="candSave" />
<candidate-edit-dlg ref="candEdit" />
<candidate-evidence-dlg ref="candEv" />
<pf-experiment-dlg ref="pfExpDlg" />
<pf-validation-dlg ref="pfValDlg" />
  `,
};

createApp(App).mount('#app');
