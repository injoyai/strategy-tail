// 根组件：侧栏 / Tabs（WAI-ARIA + URL）/ 批量任务状态条 / 功能面板 + 顶层对话框。
// 从 index.html 迁移：switchTab/键盘导航/renderTaskProgress/showPendingTask/pollStatus/
// pollStatusOnce/stopBatchTask/dismissTaskDock/initMotion。
// 任务状态放入 store.taskDock（响应式），状态条模板渲染；done 分流钩子：
// 各工作区按任务类型载入自己的结果。
import { createApp, watch } from './vendor/vue.esm-browser.prod.js';
import { state, runtime } from './store.js';
import { Api } from './api.js';
import { TABS, NAV_GROUPS, TAB_SLUG, TAB_NUM, TASK_LABEL } from './constants.js';
import { taskProgress } from './format.js';
import { animatePanel, animateStatus, initMotion } from './motion.js';
import Tab1Run from './components/Tab1Run.js';
import DataUpdate from './components/DataUpdate.js';
import RunConfigDlg from './components/RunConfigDlg.js';
import Tab2Compare from './components/Tab2Compare.js';
import Tab3Trades from './components/Tab3Trades.js';
import Tab4Factor from './components/Tab4Factor.js';
import Tab5Candidates from './components/Tab5Candidates.js';
import Tab6Portfolio from './components/Tab6Portfolio.js';
import Tab7Practice from './components/Tab7Training.js';
import Tab8Screen from './components/Tab8Screen.js';
import CandidateSaveDlg from './components/CandidateSaveDlg.js';
import CandidateEditDlg from './components/CandidateEditDlg.js';
import CandidateEvidenceDlg from './components/CandidateEvidenceDlg.js';
import PfExperimentDlg from './components/PfExperimentDlg.js';
import PfValidationDlg from './components/PfValidationDlg.js';

const App = {
  name: 'App',
  components: {
    Tab1Run, DataUpdate, RunConfigDlg, Tab2Compare, Tab3Trades, Tab4Factor, Tab5Candidates, Tab6Portfolio,
    Tab7Practice, Tab8Screen, CandidateSaveDlg, CandidateEditDlg, CandidateEvidenceDlg, PfExperimentDlg, PfValidationDlg,
  },
  data() {
    return {state, tabs: TABS, groups: NAV_GROUPS, groupTabs: {backtest: 1, factor: 4, practice: 7, screen: 8}, navOpen: false, sellEvents: [], sellCursor: 0, sellSocket: null, sellReconnect: null};
  },
  computed: {
    activeGroup() {
      return this.groups.find(g => g.tabs.includes(state.activeTab));
    },
    pageTitle() {
      return this.tabs.find(t => t.n === state.activeTab).title;
    },
    taskDock() {
      if (state.taskDock?.running || state.taskDock?.state === 'error') return state.taskDock;
      if (state.activeTab === 8 && state.screenPreview) {
        return this.taskDockFor({...state.screenPreview, state: 'running', task: 'screen_scan',
          phase: state.screenPreview.total ? 'scan' : 'preparing', unit: 'stocks', background: true});
      }
      return state.taskDock;
    },
  },
  provide() {
    return {
      switchTab: n => this.switchTab(n),
      startTask: (task, opts) => this.startTask(task, opts),
      openRunConfig: (mode, run) => this.$refs.runConfig.openDlg(mode, run),
      openCandidateDlg: () => this.$refs.candSave && this.$refs.candSave.openDlg(),
      openCandidateEdit: c => this.$refs.candEdit && this.$refs.candEdit.openDlg(c),
      openCandidateEvidence: c => this.$refs.candEv && this.$refs.candEv.openDlg(c),
      openPfExpDlg: m => this.$refs.pfExpDlg && this.$refs.pfExpDlg.openDlg(m),
      openPfValDlg: m => this.$refs.pfValDlg && this.$refs.pfValDlg.openDlg(m),
      getPfGates: () => this.$refs.tab6 && this.$refs.tab6.getPfGates(),
    };
  },
  mounted() {
    Api.stockNames().then(names => {
      state.stockNames = names;
      if (state.status?.state === 'running' && state.taskDock) state.taskDock.current = taskProgress(state.status, names).current;
    }).catch(e => console.warn('股票名称读取失败，保留代码显示', e));
    // 刷新只触发一次轮询；任务运行中由 pollStatusOnce 续上轮询
    this.pollStatusOnce();
    // 任务卡首次出现时做揭示动画
    watch(() => this.taskDock, (v, old) => {
      if (v && !old) this.$nextTick(() => animateStatus(this.$refs.statusbar));
    });
    initMotion();
    try { this.sellCursor = Number(sessionStorage.getItem('lab-screen-sell-cursor')) || 0; } catch {}
    this.connectScreenEvents();
    // Tab 状态从 URL 恢复；无参数时进入自由搭建，不自动运行任务。
    const initial = TAB_NUM[new URLSearchParams(location.search).get('tab')];
    if (initial) this.switchTab(initial);
  },
  beforeUnmount() {
    clearTimeout(this.sellReconnect);
    if (this.sellSocket) {
      this.sellSocket.onclose = null;
      this.sellSocket.close();
    }
  },
  methods: {
    connectScreenEvents() {
      const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
      const socket = new WebSocket(protocol + '//' + location.host + '/api/screen/events?after=' + this.sellCursor);
      this.sellSocket = socket;
      socket.onmessage = message => {
        let event;
        try { event = JSON.parse(message.data); } catch { return; }
        if (event.type !== 'sell' || event.id <= this.sellCursor) return;
        this.sellCursor = event.id;
        try { sessionStorage.setItem('lab-screen-sell-cursor', String(event.id)); } catch {}
        this.sellEvents = [event, ...this.sellEvents];
      };
      socket.onclose = () => { this.sellReconnect = setTimeout(() => this.connectScreenEvents(), 3000); };
    },
    openScreenSale(event) {
      this.switchTab(8);
      this.$nextTick(() => this.$refs.screen?.openSale(event.signal));
    },
    setTaskRef(name, el) {
      this.$refs[name] = el;
    },
    // ============ Tabs ============
    setPanelRef(el, n) {
      if (el) this.$refs['panel' + n] = el; else delete this.$refs['panel' + n];
    },
    groupTarget(group) {
      return group.home ?? this.groupTabs[group.id];
    },
    switchGroup(group) {
      this.switchTab(this.groupTarget(group));
      if (group.tabs.length === 1) this.closeNav();
    },
    switchSection(n) {
      this.switchTab(n);
      this.closeNav();
    },
    toggleNav() {
      if (this.navOpen) this.closeNav();
      else {
        this.navOpen = true;
        this.$nextTick(() => this.$refs.sidebar?.querySelector('[aria-current]')?.focus());
      }
    },
    closeNav() {
      if (!this.navOpen) return;
      this.navOpen = false;
      this.$refs.navToggle?.focus();
    },
    switchTab(n) {
      const group = NAV_GROUPS.find(g => g.tabs.includes(n));
      if (!group) return;
      this.groupTabs[group.id] = n;
      state.activeTab = n;
      if (n === 6) {
        const p = new URLSearchParams(location.search);
        p.set('tab', 'portfolio'); // 06 保留并更新扩展参数（model/revision/run/val/filter/sort/page）
        history.replaceState(null, '', '?' + p.toString());
      } else if (n === 7) {
        const p = new URLSearchParams(location.search);
        p.set('tab', 'practice'); // 07 保留扩展参数 p（不透明会话 ID，刷新恢复）
        if (state.practiceTrainingId) { p.set('t', state.practiceTrainingId); p.delete('p'); p.delete('bank'); }
        else if (state.practiceBankId) { p.set('bank', state.practiceBankId); p.delete('p'); p.delete('t'); }
        else if (state.practiceCurrentId && !p.has('t') && !p.has('bank')) p.set('p', state.practiceCurrentId);
        history.replaceState(null, '', '?' + p.toString());
      } else if (n === 1) {
        history.replaceState(null, '', location.pathname);
      } else {
        history.replaceState(null, '', '?tab=' + TAB_SLUG[n]);
      }
      this.$nextTick(() => animatePanel(this.$refs['panel' + n]));
    },
    onTabsKeydown(e, group) {
      const tabs = group.tabs;
      const i = tabs.indexOf(Number(e.target.dataset.tab));
      if (i === -1) return;
      let target = null;
      if (e.key === 'ArrowRight' || e.key === 'ArrowDown') target = tabs[(i + 1) % tabs.length];
      else if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') target = tabs[(i - 1 + tabs.length) % tabs.length];
      else if (e.key === 'Home') target = tabs[0];
      else if (e.key === 'End') target = tabs[tabs.length - 1];
      if (target) {
        e.preventDefault();
        this.switchTab(target);
        document.getElementById('tabbtn' + target)?.focus();
      }
    },

    // ============ 批量任务卡 ============
    // 回测与研究共用唯一任务栏；总量未就绪时保持不定进度。
    renderTaskProgress(s) {
      const st = s.state || 'idle';
      state.taskDock = st === 'idle' ? null : this.taskDockFor(s);
    },
    taskDockFor(s) {
      const st = s.state;
      const taskName = TASK_LABEL[s.task] || '批量任务';
      const p = taskProgress(s, state.stockNames);
      const connectionError = st === 'connection-error';
      const determinate = !connectionError && (st === 'done' || p.percent != null);
      const title = {
        running: `${taskName} · ${p.label}`,
        done: `${taskName}已完成`,
        error: `${taskName}失败`,
        stopped: `${taskName}已停止`,
        interrupted: `${taskName}意外中断`,
        'connection-error': s.resultReadError ? '结果读取失败' : '进度读取中断',
      }[st] || taskName;
      let detail = st === 'done' ? '结果已就绪' : p.counts || (st === 'running' ? `正在${p.label}…` : p.label);
      if (connectionError) detail = s.resultReadError ? '结果尚未载入' : '无法确认当前阶段与进度';
      if (st === 'interrupted') detail = '未取得本次任务结果';
      const current = connectionError ? s.resultReadError ? '后台任务已完成，请重试读取结果' : '任务可能仍在后台运行'
        : st === 'stopped' ? '已保留当前表单配置'
        : st === 'done' ? '' : p.current;
      return {
        state: connectionError || st === 'interrupted' ? 'error' : st,
        task: s.task,
        title,
        percent: st === 'done' ? '100%'
          : connectionError ? '状态未知'
          : determinate ? `本阶段 ${p.percent}%`
          : st === 'error' ? '失败' : st === 'stopped' ? '已停止' : '进行中',
        value: determinate ? (st === 'done' ? 100 : p.percent) : st === 'running' ? null : 0,
        indeterminate: !determinate && st === 'running',
        detail,
        current,
        error: s.error || '',
        running: st === 'running',
        background: s.background,
        readingResults: p.phase === 'reading_results',
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
        state.status = s;
        const running = s.state === 'running';
        const interrupted = s.state === 'idle' && runtime.prevRunning && s.task !== runtime.lastTask;
        if (s.state !== 'idle') runtime.lastTask = s.task || runtime.lastTask;
        const visibleStatus = s.state === 'idle' && runtime.prevRunning
          ? {...s, state: interrupted ? 'interrupted' : 'stopped', task: runtime.lastTask}
          : s;
        const readingResults = s.state === 'done';
        this.renderTaskProgress(readingResults
          ? {...s, state: 'running', phase: 'reading_results', done: -1, total: -1, progress: -1, currentCode: ''}
          : visibleStatus);
        state.taskRunning = running || readingResults;
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
          } else if (s.task === 'screen_history' || s.task === 'screen_scan') {
            if (this.$refs.screen) await this.$refs.screen.onTaskDone(s.task);
          } else if (s.task === 'practice_prepare') {
            // 盘感出题完成：07 页轮询通常先发现；此处作为任务条先行的加速钩子。
            if (s.runId && this.$refs.tab7) await this.$refs.tab7.practiceOnTaskDone(s.runId);
          } else {
            state.currentReport = await Api.latestReport();
            this.switchTab(2);
            // 组合回测完成后展示对照摘要。
            if (state.currentReport.source === 'simple' && state.currentReport.comparison) {
              const c = state.currentReport.comparison;
              state.runMsg = {text: `多条件组合对照已生成：基准 ${c.baselineTrades} 笔 → 组合 ${c.combinedTrades} 笔` +
                (c.retentionRate == null ? '' : `（${state.currentReport.config?.account ? '成交数比' : '保留'} ${(c.retentionRate * 100).toFixed(1)}%）`) +
                '，详见“回测结果”摘要。', ok: true};
            } else if (state.currentReport.source === 'builder') {
              state.runMsg = {text: '搭建器回测完成，已载入结果。', ok: true};
            } else {
              state.runMsg = {text: '已载入回测报告。', ok: true};
            }
          }
          this.renderTaskProgress(s);
          state.taskRunning = false;
        } else if (s.state === 'idle' && runtime.prevRunning && runtime.lastTask === 'analysis') {
          state.factorMsg = {text: interrupted ? '后台任务状态已重置，本次分析意外中断，请重新运行' : '任务已停止', ok: false};
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
        const resultReadError = state.status?.state === 'done';
        state.taskRunning = (!resultReadError && runtime.prevRunning);
        this.renderTaskProgress({
          state: 'connection-error', task: runtime.lastTask, progress: 0,
          resultReadError,
          error: resultReadError ? `任务已完成，结果读取失败：${e.message}。请重试读取。`
            : `无法读取任务进度：${e.message}。请重试；后台任务状态尚未确认。`,
        });
      }
    },
  },
  template: `
<div class="app-shell" @keydown.esc="closeNav">
  <aside id="lab-sidebar" ref="sidebar" class="lab-sidebar" :class="{'is-open': navOpen}">
    <div class="sidebar-brand">
      <div class="brand-mark" aria-hidden="true">
        <svg viewBox="0 0 32 32" fill="none"><path d="M5 24.5V17l6-6 5 5 10-10" stroke="#ecd69c" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"/><path d="M20 6h6v6" stroke="#7aa8ff" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"/><path d="M5 27h22" stroke="#ecd69c" stroke-opacity=".5" stroke-width="1.4" stroke-linecap="round"/></svg>
      </div>
      <span class="brand-copy">策略实验室</span>
    </div>
    <nav class="workspace-nav" aria-label="主导航">
      <div v-for="g in groups" :key="g.id" class="workspace-group">
        <button class="workspace-link" :id="'nav-' + g.id" type="button"
          :aria-current="activeGroup.id === g.id ? 'location' : null" :aria-controls="'tab' + groupTarget(g)"
          @click="switchGroup(g)">{{ g.title }}</button>
        <div v-if="g.tabs.length > 1" class="workspace-subnav" :role="activeGroup.id === g.id ? 'tablist' : 'group'"
          :aria-label="g.title" :aria-orientation="activeGroup.id === g.id ? 'vertical' : null"
          @keydown="activeGroup.id === g.id && onTabsKeydown($event, g)">
          <button v-for="t in tabs.filter(t => g.tabs.includes(t.n))" :key="t.n" class="workspace-link" type="button"
            :role="activeGroup.id === g.id ? 'tab' : null"
            :id="'tabbtn' + t.n" :data-tab="t.n" :aria-controls="'tab' + t.n"
            :aria-selected="activeGroup.id === g.id ? String(state.activeTab === t.n) : null"
            :tabindex="activeGroup.id === g.id && state.activeTab !== t.n ? -1 : 0"
            @click="switchSection(t.n)">{{ t.navTitle || t.title }}</button>
        </div>
      </div>
    </nav>
  </aside>
  <div class="workspace-body">
    <header class="masthead">
      <button ref="navToggle" class="secondary workspace-toggle" type="button" aria-controls="lab-sidebar"
        :aria-expanded="String(navOpen)" @click="toggleNav">菜单</button>
      <h1>{{ pageTitle }}</h1>
    </header>
<main :class="{'practice-page': state.activeTab === 7 && !taskDock, 'has-task-dock': taskDock}">
  <data-update />
  <section v-if="sellEvents.length" class="screen-sale-notices card" aria-label="卖点提醒">
    <div class="screen-result-head">
      <strong role="status" aria-live="polite">{{ sellEvents.length }} 条卖点提醒</strong>
      <button type="button" class="secondary" @click="sellEvents = []">清除提醒</button>
    </div>
    <ul>
      <li v-for="event in sellEvents" :key="event.id">
        <span>{{ event.signal.name || event.signal.code }} · {{ event.strategyName }} · {{ event.signal.sellTime }} · {{ event.signal.sellPrice.toFixed(2) }} 元</span>
        <button type="button" class="secondary" @click="openScreenSale(event)">查看K线</button>
      </li>
    </ul>
  </section>
  <section class="statusbar" ref="statusbar" v-show="!!taskDock" :data-state="taskDock ? taskDock.state : 'idle'"
    aria-live="polite" aria-atomic="true" aria-labelledby="statusState">
    <div class="status-head">
      <div class="status-title-wrap">
        <span class="status-dot" aria-hidden="true"></span>
        <span id="statusState">{{ taskDock?.title || '' }}</span>
      </div>
      <span id="statusPercent" class="status-percent">{{ taskDock?.percent || '' }}</span>
    </div>
    <div class="progress-outer">
      <progress class="progress-inner" :class="{indeterminate: taskDock?.indeterminate}"
        :max="100" :value="taskDock ? taskDock.value : 0"
        :aria-label="taskDock ? taskDock.title + '进度' : '批量任务进度'"
        :aria-valuetext="taskDock ? taskDock.detail + '，' + taskDock.percent : ''">{{ taskDock?.percent || '' }}</progress>
    </div>
    <div class="status-foot">
      <div class="status-meta">
        <span id="statusDetail" class="status-detail">{{ taskDock?.detail || '' }}</span>
        <span id="statusCurrent" class="status-current">{{ taskDock?.current || '' }}</span>
      </div>
      <div class="status-actions">
        <button type="button" class="secondary" v-if="taskDock?.connectionError" @click="pollStatus()">重试读取</button>
        <button type="button" class="danger" v-if="taskDock?.running && !taskDock.readingResults && !taskDock.background" @click="stopBatchTask" :disabled="state.stopBusy" :aria-busy="String(state.stopBusy)">停止任务</button>
        <button type="button" class="secondary" v-if="taskDock && !taskDock.running" @click="dismissTaskDock()">关闭</button>
      </div>
    </div>
    <details class="ui-help error-details" v-if="taskDock?.error">
      <summary>错误详情</summary>
      <div class="msg">{{ taskDock.error }}</div>
    </details>
  </section>

  <div v-for="t in tabs" :key="t.n" class="tab-panel" :id="'tab' + t.n" :ref="el => setPanelRef(el, t.n)"
    :role="t.n >= 7 ? 'region' : 'tabpanel'" :aria-labelledby="t.n === 7 ? 'nav-practice' : t.n === 8 ? 'nav-screen' : 'tabbtn' + t.n"
    tabindex="0" v-show="state.activeTab === t.n">
    <tab1-run v-if="t.n === 1" />
    <tab2-compare v-if="t.n === 2" />
    <tab3-trades v-if="t.n === 3" />
    <tab4-factor v-if="t.n === 4" />
    <tab5-candidates v-if="t.n === 5" />
    <tab6-portfolio :ref="el => setTaskRef('tab6', el)" v-if="t.n === 6" />
    <tab7-practice :ref="el => setTaskRef('tab7', el)" v-if="t.n === 7" />
    <tab8-screen v-if="t.n === 8" :ref="el => setTaskRef('screen', el)" :active="state.activeTab === 8" :sale-revision="sellCursor" />
  </div>
</main>
  </div>
</div>

<candidate-save-dlg ref="candSave" />
<candidate-edit-dlg ref="candEdit" />
<candidate-evidence-dlg ref="candEv" />
<pf-experiment-dlg ref="pfExpDlg" />
<pf-validation-dlg ref="pfValDlg" />
<run-config-dlg ref="runConfig" />
  `,
};

createApp(App).mount('#app');
