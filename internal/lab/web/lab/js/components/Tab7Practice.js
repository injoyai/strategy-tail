// Tab7Practice：盘感练习（07 页签）。
// 出题（复用已保存策略买入条件）→ 盲练（隐藏身份与真实日期、按揭示日收盘
// 决策）→ 卖后逐日观察 → 结束后复盘与同题重练。状态与成交一律以服务端
// 持久状态为准：前端只提交动作 + 命令幂等 ID + 期望版本，不本地重算。
// 防护约定：旧响应不覆盖新会话（seq/histSeq/reviewSeq + 会话 ID 比对）、
// 网络失败保留命令待重试（同 commandId 服务端幂等）、重复点击由 actionBusy
// 挡住、preparing 用组件内 2s 轮询独立发现完成/失败/取消。
import { watch } from '../vendor/vue.esm-browser.prod.js';
import { state } from '../store.js';
import { Api } from '../api.js';
import { practicePhaseLabel, practiceStageLabel, practiceEndReasonLabel, practiceFailReasonLabel, practiceActionLabel, practiceDayLabel, newCommandId, pnlClass, fmtMoney, fmtPct, fmtTime, practiceKlineData, practiceReportSections, practiceReportSummary, practiceReportText } from '../practice.js';
import { mountKline, dropKline } from '../kline.js';

export default {
  name: 'Tab7Practice',
  props: {training: {default: null}, initialSessionId: {default: ''}, historyOnly: {default: false}},
  emits: ['training-update', 'next-question', 'retake', 'home'],
  watch: { 'training.revision'() { if (this.training) this.applySession(this.training.session); } },
  inject: ['switchTab', 'startTask'],
  setup() {
    // 模板中直接调用的格式化与文案函数必须从 setup 返回，否则模板作用域不可见。
    return {
      state,
      fmtMoney, fmtPct, fmtTime, pnlClass, practiceReportSections, practiceReportSummary,
      practicePhaseLabel, practiceStageLabel, practiceEndReasonLabel,
      practiceFailReasonLabel, practiceActionLabel, practiceDayLabel,
    };
  },
  data() {
    return {
      // —— 出题表单 ——
      pStrategyId: '',
      pFromYear: 2020,
      pToYear: new Date().getFullYear(),
      pRequestId: '',          // 出题请求幂等键：网络失败保留，重试同键
      pStarting: false,
      startMsg: null,

      // —— 会话 ——
      sess: null,              // 服务端会话视图（唯一事实源）
      sessLoading: false,
      sessErr: '',
      seq: 0,                  // 会话请求序号：旧响应不覆盖
      pollTimer: null,         // preparing 轮询（2s GET）
      pollErr: '',

      // —— 动作 ——
      actionBusy: false,
      actionMsg: null,
      pendingCmd: null,        // 网络中断待确认的命令（重试复用同 commandId）
      confirmAction: '',       // 两步确认：skip | finish
      confirmTimer: null,

      // —— 历史 ——
      history: [],
      histLoading: false,
      histErr: '',
      histSeq: 0,
      retryMsg: null,

      // —— 复盘 ——
      view: 'session',         // session | review
      reviewMode: 'report',    // report | chart
      review: null,
      reviewLoading: false,
      reviewErr: '',
      reviewSeq: 0,
      retryRequestId: '',
      retrySourceId: '',
      retrying: false,

      // —— 图表 ——
      klineC: null,
      reviewC: null,
      klineWarning: '',
    };
  },
  computed: {
    currentYear() { return new Date().getFullYear(); },
    positionText() {
      const s = this.sess;
      if (!s) return '';
      if (s.stage === 'waiting') return '未买入';
      if (s.stage === 'holding') return `${s.quantity} 股 · 成本 ${fmtMoney(s.buyCostYuan)} 元`;
      if (s.stage === 'sold') return `${s.quantity} 股已卖出 · 回收 ${fmtMoney(s.sellIncomeYuan)} 元`;
      return '—';
    },
    reviewTitle() {
      const rv = this.review;
      if (!rv) return '';
      const sig = (rv.days || []).find(d => d.day === 0);
      return `${rv.code} · 信号日 ${sig && sig.date ? sig.date : '—'}`;
    },
    // 本题评估行（口径全部来自服务端 eval，前端不重算）。
    evalRows() {
      const s = this.sess;
      if (!s || !s.eval || s.stage === 'waiting') return [];
      const e = s.eval;
      const rows = [];
      if (s.stage === 'holding') {
        rows.push({label: '浮动盈亏', value: `${fmtMoney(e.openPnl, true)} 元（${fmtPct(e.openPct)}）`, cls: pnlClass(e.openPnl)});
      }
      if (s.stage === 'sold') {
        rows.push({label: '已实现盈亏', value: `${fmtMoney(e.realizedPnl, true)} 元（${fmtPct(e.realizedPct)}）`, cls: pnlClass(e.realizedPnl)});
      }
      rows.push({label: '持仓', value: `${e.holdingDays} 个交易日`, cls: ''});
      rows.push({label: '最大浮盈', value: `${fmtMoney(e.maxFloatProfit, true)} 元（${fmtPct(e.maxFloatProfitPct)}）`, cls: pnlClass(e.maxFloatProfit)});
      rows.push({label: '最大浮亏', value: `${fmtMoney(e.maxFloatLoss, true)} 元（${fmtPct(e.maxFloatLossPct)}）`, cls: pnlClass(e.maxFloatLoss)});
      return rows;
    },
  },
  mounted() {
    if (this.training) { this.applySession(this.training.session); return; }
    this.loadHistory();
    this.ensureStrategies();
    // 切回本页时按实际容器尺寸重绘（v-show 切页不触发窗口 resize）。
    watch(() => state.activeTab, n => {
      if (n !== 7) return;
      this.$nextTick(() => {
        if (this.klineC) this.klineC.resize();
        if (this.reviewC) this.reviewC.resize();
      });
    });
    // 恢复：URL ?p=（不透明会话 ID）优先，其次本页保留的当前会话。
    const pid = new URLSearchParams(location.search).get('p');
    const id = this.initialSessionId || pid || (this.historyOnly ? null : state.practiceCurrentId);
    if (id) this.loadSession(id);
  },
  unmounted() {
    this.stopPoll();
    clearTimeout(this.confirmTimer);
    this.seq++; this.histSeq++; this.reviewSeq++;
    dropKline(this.klineC); this.klineC = null;
    dropKline(this.reviewC); this.reviewC = null;
  },
  methods: {
    // ============ URL 与会话 ============
    syncUrl(id) {
      if (this.training) return;
      if (state.activeTab !== 7) return;
      const p = new URLSearchParams(location.search);
      if (id) { p.set('tab', 'practice'); p.set('p', id); }
      else p.delete('p');
      history.replaceState(null, '', '?' + p.toString());
    },
    async loadSession(id) {
      const request = ++this.seq;
      this.stopPoll();
      if (this.sess?.id !== id) this.pendingCmd = null;
      this.sessLoading = true;
      this.sessErr = '';
      try {
        const result = this.training ? await Api.training(this.training.id) : await Api.practice(id);
        if (request !== this.seq) return;
        if (this.training) this.$emit('training-update', result);
        this.applySession(this.training ? result.session : result);
      } catch (e) {
        if (request !== this.seq) return;
        this.sessLoading = false;
        if (e.status === 404) {
          this.clearSession();
          this.sessErr = '该练习不存在或已被删除';
        } else {
          this.sessErr = '练习状态载入失败：' + e.message;
        }
      }
    },
    applySession(s) {
      if (this.sess?.id === s.id && (s.revision ?? 0) < (this.sess.revision ?? 0)) {
        this.sessLoading = false;
        return;
      }
      const refreshHistory = this.sess?.id !== s.id || this.sess?.phase !== s.phase || this.sess?.stage !== s.stage;
      this.stopPoll();
      if (this.sess?.id !== s.id) {
        this.reviewSeq++;
        this.reviewLoading = false;
        this.review = null;
        this.view = 'session';
      }
      this.sess = s;
      this.sessLoading = false;
      this.sessErr = '';
      this.pollErr = '';
      this.actionMsg = null;
      this.confirmAction = '';
      if (!this.training) state.practiceCurrentId = s.id;
      this.syncUrl(s.id);
      if (refreshHistory && !this.training) this.loadHistory();
      if (s.phase === 'preparing') {
        this.startTask('practice_prepare', {phase: 'prepare', runId: s.id});
        this.startPoll();
      } else if ((s.phase === 'active' || s.phase === 'finished') && this.view === 'session') {
        this.$nextTick(() => this.renderKline());
      }
    },
    clearSession() {
      if (this.training) { this.$emit('home'); return; }
      this.seq++; this.reviewSeq++;
      this.sessLoading = false; this.reviewLoading = false;
      this.pendingCmd = null;
      this.stopPoll();
      clearTimeout(this.confirmTimer);
      this.confirmAction = '';
      this.sess = null;
      this.review = null;
      this.view = 'session';
      this.klineWarning = '';
      state.practiceCurrentId = '';
      this.syncUrl('');
    },
    // 任务完成钩子（app.js done 链）：任务条轮询先于本组件发现时加速切换。
    async practiceOnTaskDone(sessionId) {
      if (this.sess && this.sess.id === sessionId && this.sess.phase === 'preparing') {
        this.stopPoll();
        await this.loadSession(sessionId);
      } else {
        this.loadHistory();
      }
    },

    // ============ preparing 轮询 ============
    startPoll() {
      this.stopPoll();
      this.pollTimer = setInterval(() => this.pollOnce(), 2000);
    },
    stopPoll() {
      if (this.pollTimer) { clearInterval(this.pollTimer); this.pollTimer = null; }
    },
    async pollOnce() {
      const id = this.sess && this.sess.id;
      if (!id) { this.stopPoll(); return; }
      const request = this.seq;
      try {
        const s = await Api.practice(id);
        if (request !== this.seq || !this.sess || this.sess.id !== id) return;
        if (s.phase === 'preparing') { this.sess = s; this.pollErr = ''; return; }
        // 覆盖 done/error 任务条通知缺口：完成、失败、取消都在此发现。
        this.stopPoll();
        this.applySession(s);
      } catch (e) {
        if (request !== this.seq || this.sess?.id !== id) return;
        this.pollErr = `出题进度查询失败：${e.message}；将继续重试。`;
      }
    },

    // ============ 出题 ============
    async ensureStrategies(force) {
      if (!force && state.builderStrategies.length) return;
      try { state.builderStrategies = (await Api.builderStrategies()).strategies || []; }
      catch (e) { this.startMsg = {text: '策略列表加载失败: ' + e.message, ok: false}; }
    },
    async startPractice() {
      if (this.pStarting) return;
      const st = state.builderStrategies.find(s => s.id === this.pStrategyId);
      if (!st) { this.startMsg = {text: '请选择已保存的策略', ok: false}; return; }
      this.pStarting = true;
      this.startMsg = null;
      const request = this.seq;
      const rid = this.pRequestId || newCommandId();
      this.pRequestId = rid; // 网络失败时保留，同键重试服务端幂等
      try {
        const res = await Api.practiceStart({
          strategyId: this.pStrategyId,
          strategyVersion: st.version,
          fromYear: Number(this.pFromYear),
          toYear: Number(this.pToYear),
          requestId: rid,
        });
        this.pRequestId = '';
        if (request !== this.seq) {
          this.loadHistory();
          return;
        }
        this.clearSession();
        this.view = 'session';
        await this.loadSession(res.sessionId);
      } catch (e) {
        if (e.status == null) {
          this.startMsg = {text: `出题请求未确认（${e.message}）。可直接重试：同一请求不会重复出题。`, ok: false};
        } else if (e.status === 409) {
          // 任务忙 / 策略版本已更新：刷新策略列表并提示，请求键作废。
          this.pRequestId = '';
          this.ensureStrategies(true);
          this.startMsg = {text: e.message, ok: false};
        } else {
          this.pRequestId = '';
          this.startMsg = {text: e.message, ok: false};
        }
      } finally {
        this.pStarting = false;
      }
    },

    async nextPractice() {
      if (this.training) { this.$emit('next-question'); return; }
      if (this.pStarting || this.retrying || this.sess?.phase !== 'finished') return;
      const sess = this.sess, request = this.seq;
      this.pStarting = true;
      this.startMsg = null;
      try {
        // 新题使用策略当前保存版本；旧题及同题重练继续保留原快照。
        const res = await Api.builderStrategies();
        if (request !== this.seq) return;
        state.builderStrategies = res.strategies || [];
        if (!state.builderStrategies.some(s => s.id === sess.strategyId)) {
          this.startMsg = {text: '本题策略已被删除，请返回出题页选择其他策略。', ok: false};
          return;
        }
        this.pStrategyId = sess.strategyId;
        this.pFromYear = sess.fromYear;
        this.pToYear = sess.toYear;
      } catch (e) {
        if (request === this.seq) this.startMsg = {text: '读取下一题策略失败：' + e.message, ok: false};
        return;
      } finally {
        this.pStarting = false;
      }
      if (request === this.seq) await this.startPractice();
    },

    // ============ 动作（买入/卖出/推进/放弃/结束） ============
    async doAction(action) {
      if (this.actionBusy || this.sessLoading || !this.sess || this.sess.phase !== 'active') return;
      const s = this.sess;
      const request = this.seq;
      const cmd = (this.pendingCmd && this.pendingCmd.action === action)
        ? this.pendingCmd
        : {action, commandId: newCommandId(), expectedRevision: this.training ? this.training.revision : s.revision, ...(this.training ? {questionId: s.id} : {})};
      this.actionBusy = true;
      this.actionMsg = null;
      try {
        const result = this.training ? await Api.trainingAction(this.training.id, cmd) : await Api.practiceAction(s.id, cmd);
        const updated = this.training ? result.session : result;
        if (request !== this.seq || !this.sess || this.sess.id !== s.id) return;
        this.pendingCmd = null;
        this.applySession(updated);
        if (this.training) this.$emit('training-update', result);
        if (!this.training && updated.phase === 'finished') await this.openReview();
      } catch (e) {
        if (request !== this.seq || !this.sess || this.sess.id !== s.id) return;
        if (e.status == null) {
          // 网络中断：保留完整命令（同 commandId 幂等），结果未确认。
          this.pendingCmd = cmd;
          this.actionMsg = {text: `「${practiceActionLabel(action)}」提交结果未确认：${e.message}。可直接重试，不会重复成交。`, ok: false};
        } else if (e.status === 409) {
          // 版本冲突 / 命令冲突 / 幂等重放后状态已推进：刷新权威状态。
          this.pendingCmd = null;
          this.actionMsg = {text: '状态已变化：' + e.message + '，正在刷新…', ok: false};
          await this.loadSession(s.id);
        } else {
          this.pendingCmd = null;
          this.actionMsg = {text: e.message, ok: false};
        }
      } finally {
        this.actionBusy = false;
      }
    },
    discardPending() {
      this.pendingCmd = null;
      this.actionMsg = null;
      if (this.sess && this.sess.id) this.loadSession(this.sess.id);
    },
    askConfirm(kind) {
      if (this.confirmAction === kind) {
        this.confirmAction = '';
        this.doAction(kind);
        return;
      }
      this.confirmAction = kind;
      clearTimeout(this.confirmTimer);
      this.confirmTimer = setTimeout(() => { this.confirmAction = ''; }, 6000);
    },

    // ============ 历史 ============
    async loadHistory() {
      const request = ++this.histSeq;
      this.histLoading = true;
      this.histErr = '';
      try {
        const res = await Api.practices();
        if (request !== this.histSeq) return;
        this.history = res.practices || [];
        this.histLoading = false;
      } catch (e) {
        if (request !== this.histSeq) return;
        this.histLoading = false;
        this.histErr = e.message;
      }
    },
    openPractice(h) {
      this.view = 'session';
      this.review = null;
      this.klineWarning = '';
      this.loadSession(h.id);
    },

    // ============ 复盘 ============
    async openReview() {
      const id = this.sess && this.sess.id;
      if (!id || this.reviewLoading) return;
      const request = ++this.reviewSeq;
      this.view = 'review';
      this.reviewMode = 'report';
      this.reviewLoading = true;
      this.reviewErr = '';
      try {
        const rv = this.training ? await Api.trainingReview(this.training.id, id) : await Api.practiceReview(id);
        if (request !== this.reviewSeq || (this.sess && this.sess.id) !== id) return;
        this.review = rv;
        this.reviewLoading = false;
        await this.$nextTick();
        if (request === this.reviewSeq && this.reviewMode === 'chart') this.renderReview();
      } catch (e) {
        if (request !== this.reviewSeq) return;
        this.reviewLoading = false;
        this.reviewErr = e.message;
      }
    },
    showReviewMode(mode) {
      this.reviewMode = mode;
      if (mode === 'chart') this.$nextTick(() => this.renderReview());
    },
    downloadReport() {
      if (!this.review?.report) return;
      const url = URL.createObjectURL(new Blob([practiceReportText(this.review)], {type: 'text/plain;charset=utf-8'}));
      const link = document.createElement('a');
      link.href = url;
      link.download = `练习分析报告-${this.review.id}.txt`;
      link.click();
      setTimeout(() => URL.revokeObjectURL(url), 0);
    },
    backToSession() {
      this.view = 'session';
      this.klineWarning = '';
      this.$nextTick(() => this.renderKline());
    },

    // ============ 重练（另建记录，复用原快照） ============
    async retryById(id) {
      if (this.retrying || this.pStarting) return;
      this.retrying = true;
      this.retryMsg = null;
      const request = this.seq;
      const rid = (this.retrySourceId === id && this.retryRequestId) || newCommandId();
      this.retryRequestId = rid; // 网络失败保留，同键重试不重复建题
      this.retrySourceId = id;
      try {
        const s = this.training ? await Api.trainingRetry(this.training.id, id, rid) : await Api.practiceRetry(id, rid);
        this.retryRequestId = '';
        this.retrySourceId = '';
        if (request !== this.seq) {
          this.loadHistory();
          this.retryMsg = {text: '重练已创建，可从练习历史进入。', ok: true};
          return;
        }
        if (this.training) { this.$emit('retake', s); return; }
        this.view = 'session';
        this.review = null;
        this.klineWarning = '';
        this.applySession(s);
        this.retryMsg = {text: '已创建重练记录并切换到新练习：同题同快照，进度从信号日重新开始。', ok: true};
      } catch (e) {
        if (e.status == null) {
          this.retryMsg = {text: `重练请求未确认（${e.message}）。可直接重试，不会重复建题。`, ok: false};
        } else {
          this.retryRequestId = '';
          this.retryMsg = {text: e.message, ok: false};
        }
      } finally {
        this.retrying = false;
      }
    },
    retryPractice() {
      if (this.sess && this.sess.phase === 'finished') this.retryById(this.sess.id);
    },

    // ============ 图表 ============
    sessionMarkers() {
      const s = this.sess;
      if (!s || !s.days || !s.days.length) return [];
      const priceAt = day => {
        const d = s.days.find(x => x.day === day);
        return d ? d.close : null;
      };
      const ms = [];
      if (s.buyDay != null) {
        const p = priceAt(s.buyDay);
        if (p != null) ms.push({day: s.buyDay, price: p, kind: 'buy'});
      }
      if (s.sellDay != null) {
        const p = priceAt(s.sellDay);
        if (p != null) ms.push({day: s.sellDay, price: p, kind: 'sell'});
      } else if (s.phase === 'finished' && s.stage === 'holding' && s.buyDay != null) {
        // 未平仓结束（行情用尽或手动结束）：按结束日收盘估值的标记。
        const p = priceAt(s.currentDay);
        if (p != null) ms.push({day: s.currentDay, price: p, kind: 'est'});
      }
      return ms;
    },
    renderKline() {
      const s = this.sess;
      const el = this.$refs.klineChart;
      if (!s || !s.days || !s.days.length || !el) return;
      if (this.klineC && this.klineC.getDom() !== el) {
        dropKline(this.klineC); this.klineC = null;
      }
      if (!this.klineC) {
        this.klineC = mountKline(el);
        if (!this.klineC) { this.klineWarning = '图表组件未加载，行情图暂不可用，请刷新页面重试。'; return; }
      }
      try {
        const missing = this.klineC.setData(practiceKlineData({days: s.days, markers: this.sessionMarkers(), identity: s.id}));
        this.klineWarning = missing.length ? '部分买卖标记未能定位到已揭示行情，仅以状态摘要为准。' : '';
      } catch (e) {
        this.klineC.clear();
        this.klineWarning = '行情图绘制失败，请刷新页面重试。';
      }
    },
    renderReview() {
      const rv = this.review;
      const el = this.$refs.reviewChart;
      if (!rv || !rv.days || !rv.days.length || !el) return;
      if (this.reviewC && this.reviewC.getDom() !== el) {
        dropKline(this.reviewC); this.reviewC = null;
      }
      if (!this.reviewC) {
        this.reviewC = mountKline(el);
        if (!this.reviewC) { this.klineWarning = '图表组件未加载，复盘图暂不可用，以下方成交明细为准。'; return; }
      }
      const markers = (rv.trades || []).map(t => ({day: t.day, price: t.price, kind: t.action === 'buy' ? 'buy' : 'sell'}));
      try {
        const missing = this.reviewC.setData(practiceKlineData({days: rv.days, markers, showDate: true, identity: this.sess?.id}));
        this.klineWarning = missing.length ? '部分成交标记未能定位到复盘窗口行情，仅以下方明细为准。' : '';
      } catch (e) {
        this.reviewC.clear();
        this.klineWarning = '复盘图绘制失败，请刷新页面重试，成交以下方明细为准。';
      }
    },
  },
  template: `
<div class="practice-layout" :class="{'practice-workspace': sess && ['active', 'finished'].includes(sess.phase)}">
  <div v-if="sessErr" class="warnbar" role="alert">{{ sessErr }}
    <button type="button" class="secondary" @click="clearSession">{{ training ? '返回题库' : '返回出题' }}</button>
  </div>

  <!-- 出题表单：仅在无会话且非载入中显示 -->
  <section v-if="!sess && !sessLoading && !historyOnly" class="card">
    <h3>开始一道练习</h3>
    <p class="hint">选择一个已保存策略，仅用其买入条件在年份范围内的全部历史命中机会中等概率抽题；练习中隐藏股票身份与真实日期，按当前可见交易日收盘价模拟成交，每题预算 {{ fmtMoney(20000) }} 元（含买入费用、100 股取整、零滑点），买入当天不能卖出。</p>
    <div class="filters">
      <select v-model="pStrategyId" aria-label="选择策略">
        <option value="" disabled>选择已保存策略</option>
        <option v-for="s in state.builderStrategies" :key="s.id" :value="s.id">{{ s.name }}（v{{ s.version }}）</option>
      </select>
      <input type="number" v-model="pFromYear" min="1990" :max="currentYear" aria-label="起始年份" style="width:92px">
      <span class="hint" style="margin:0">至</span>
      <input type="number" v-model="pToYear" min="1990" :max="currentYear" aria-label="结束年份" style="width:92px">
      <button type="button" class="primary" :disabled="pStarting || !pStrategyId" @click="startPractice">{{ pStarting ? '提交中…' : '开始出题' }}</button>
    </div>
    <p v-if="startMsg" class="msg" :class="startMsg.ok ? 'ok' : 'err'" role="status">{{ startMsg.text }}</p>
    <p v-if="!state.builderStrategies.length" class="hint">尚未保存任何策略：请先在「回测配置」页用策略搭建器保存策略。</p>
  </section>

  <!-- 会话载入中 -->
  <p v-if="sessLoading" class="empty" role="status">载入练习…</p>

  <template v-else-if="sess">
    <!-- 出题中 -->
    <section v-if="sess.phase === 'preparing'" class="card">
      <h3>正在出题 · {{ sess.strategyName || '所选策略' }} · {{ sess.fromYear }}–{{ sess.toYear }}</h3>
      <p class="hint">需要扫描完整股票池，保证所有命中机会等概率入选；扫描完成后自动进入练习。可以离开本页，刷新后继续查看进度。</p>
      <div class="filters practice-prepare-actions">
        <button type="button" class="secondary" @click="clearSession">返回出题页（后台继续）</button>
        <button type="button" class="secondary" @click="loadSession(sess.id)">刷新练习状态</button>
      </div>
      <p v-if="pollErr" class="msg err" role="alert">{{ pollErr }}</p>
    </section>

    <!-- 出题失败 / 取消 -->
    <section v-else-if="sess.phase === 'failed' || sess.phase === 'cancelled'" class="card">
      <h3>{{ sess.phase === 'cancelled' ? '已停止出题' : '出题未完成' }}</h3>
      <p class="msg" :class="sess.phase === 'cancelled' ? '' : 'err'" role="status">{{ sess.phase === 'cancelled' ? '扫描已停止，可调整策略或年份后重新出题。' : practiceFailReasonLabel(sess.failReason) }}</p>
      <div class="filters" style="margin-top:12px">
        <button type="button" class="primary" @click="clearSession">返回重新出题</button>
      </div>
    </section>

    <!-- 进行中 / 已结束 -->
    <template v-else>
      <section class="card practice-toolbar">
        <div class="practice-stats" role="group" aria-label="练习状态">
          <span>策略<b>{{ sess.strategyName }}</b></span>
          <span v-if="sess.sourceSessionId">类型<b>同题重练</b></span>
          <span>范围<b>{{ sess.fromYear }}–{{ sess.toYear }}</b></span>
          <span>预算<b>{{ fmtMoney(sess.budgetYuan) }} 元</b></span>
          <span>位置<b>{{ practiceDayLabel(sess.currentDay) }}</b></span>
          <span>收盘<b>{{ fmtMoney(sess.currentPrice) }}</b></span>
          <span>仓位<b>{{ positionText }}</b></span>
          <span v-if="sess.phase === 'finished'">结果<b>{{ practiceEndReasonLabel(sess.endReason) || '已结束' }}</b></span>
        </div>
        <div class="filters practice-actions">
          <template v-if="sess.phase === 'active'">
            <button v-if="sess.stage === 'waiting'" type="button" class="primary" :disabled="actionBusy || !sess.canBuy" @click="doAction('buy')">买入</button>
            <button v-if="sess.stage === 'holding'" type="button" class="primary" :disabled="actionBusy || !sess.canSell" @click="doAction('sell')">卖出</button>
            <button type="button" class="secondary" :disabled="actionBusy" @click="doAction('next')">{{ sess.canNext ? '下一交易日' : '行情用尽，结束本题' }}</button>
            <button v-if="sess.stage === 'waiting'" type="button" class="danger" :disabled="actionBusy || !sess.canSkip" @click="askConfirm('skip')">{{ confirmAction === 'skip' ? '再点一次确认放弃' : '放弃本题' }}</button>
            <button type="button" class="danger" :disabled="actionBusy || !sess.canFinish" @click="askConfirm('finish')">{{ confirmAction === 'finish' ? '再点一次确认结束' : '结束本题' }}</button>
          </template>
          <template v-else-if="sess.phase === 'finished'">
            <button v-if="!historyOnly && !training" type="button" class="primary" :disabled="pStarting || retrying" @click="nextPractice">{{ pStarting ? '正在出下一题…' : '开始下一题' }}</button>
            <button v-if="view === 'session'" type="button" class="secondary" @click="openReview">{{ training ? '单题分析 / 复盘' : '分析报告 / 复盘' }}</button>
            <button v-else type="button" class="secondary" @click="backToSession">返回练习视图</button>
            <button type="button" class="secondary" :disabled="retrying || pStarting" @click="retryPractice">{{ retrying ? '重练中…' : '同题重练' }}</button>
          </template>
          <button type="button" class="secondary" :disabled="actionBusy || retrying || pStarting" @click="clearSession">{{ training ? '返回题库' : '返回出题' }}</button>
        </div>
        <p v-if="sess.phase === 'active' && !sess.canNext" class="hint">已无后续行情，仍可按规则在当日买卖；点「行情用尽，结束本题」结束练习（未平仓只估值，不虚构卖出）。</p>
        <p v-if="startMsg" class="msg" :class="startMsg.ok ? 'ok' : 'err'" role="status">{{ startMsg.text }}</p>
        <p v-if="actionMsg" class="msg" :class="actionMsg.ok ? 'ok' : 'err'" role="status">{{ actionMsg.text }}</p>
        <div v-if="pendingCmd" class="warnbar" role="alert">
          上一次「{{ practiceActionLabel(pendingCmd.action) }}」提交结果未确认（网络中断）。重试使用同一命令编号，不会重复成交。
          <button type="button" class="primary" :disabled="actionBusy" @click="doAction(pendingCmd.action)">重试提交</button>
          <button type="button" class="secondary" :disabled="actionBusy" @click="discardPending">放弃并刷新状态</button>
        </div>
        <div v-if="evalRows.length && (view !== 'review' || reviewMode !== 'report')" class="practice-stats" role="group" aria-label="本题评估">
          <span v-for="r in evalRows" :key="r.label">{{ r.label }}<b :class="r.cls">{{ r.value }}</b></span>
        </div>
      </section>

      <!-- 练习视图：已揭示行情（盲练，无真实日期） -->
      <section v-if="view === 'session'" class="card practice-chart-panel">
        <h3>已揭示行情 · {{ practiceDayLabel(sess.currentDay) }} 收盘 {{ fmtMoney(sess.currentPrice) }}</h3>
        <details class="ui-help practice-chart-help"><summary>图表说明</summary><p class="hint">横轴为相对信号日的交易日序号，不含真实日期与股票身份；B=买入、S=卖出、估=结束估值。买入标记在当日 K 线下方，卖出及估值在上方，准确价格见标记提示。MACD 参数 12/26/9，柱=DIF−DEA。卖出后仍可逐日推进观察，已实现收益保持不变。</p></details>
        <p v-if="klineWarning" class="hint" role="status">{{ klineWarning }}</p>
        <div ref="klineChart" class="chart-kline" role="img" aria-label="已揭示日K、成交量、MACD 图；横轴为相对信号日序号，不含真实日期与股票身份"></div>
      </section>

      <!-- 复盘视图：结束后披露身份与真实日期 -->
      <section v-else class="card practice-chart-panel" :class="{'practice-report-panel': reviewMode === 'report'}">
        <h3>{{ reviewMode === 'report' ? '分析报告' : '复盘' }} · {{ reviewTitle || '载入中' }}</h3>
        <p v-if="reviewLoading" class="empty" role="status">载入复盘…</p>
        <div v-else-if="reviewErr" class="warnbar" role="alert">复盘加载失败：{{ reviewErr }}
          <button type="button" class="secondary" @click="openReview">重试</button>
        </div>
        <template v-else-if="review">
          <div class="practice-review-actions" role="group" aria-label="复盘内容">
            <button type="button" class="secondary" :aria-pressed="String(reviewMode === 'report')" @click="showReviewMode('report')">分析报告</button>
            <button type="button" class="secondary" :aria-pressed="String(reviewMode === 'chart')" @click="showReviewMode('chart')">K 线复盘</button>
            <button v-if="reviewMode === 'report' && review.report" type="button" class="secondary" @click="downloadReport">下载报告</button>
          </div>
          <div v-if="reviewMode === 'report'" class="practice-report-body">
            <p class="hint" role="status">{{ practiceReportSummary(review) }}</p>
            <p v-if="review.report" class="hint">报告区间 {{ review.report.startDate }} 至 {{ review.report.endDate }}</p>
            <div class="practice-report-sections">
              <section v-for="section in practiceReportSections(review)" :key="section.title">
                <h4>{{ section.title }}</h4>
                <dl><template v-for="row in section.rows" :key="row.label"><dt>{{ row.label }}</dt><dd :class="row.cls">{{ row.value }}</dd></template></dl>
              </section>
            </div>
            <details v-if="review.report?.points?.length" class="ui-help">
              <summary>逐日资金与收益（{{ review.report.points.length }} 个交易日）</summary>
              <div class="detail-wrap"><table>
                <thead><tr><th>日期</th><th>总资金（元）</th><th>累计盈亏（元）</th><th>整题回报</th></tr></thead>
                <tbody><tr v-for="p in review.report.points" :key="p.date"><td>{{ p.date }}</td><td>{{ fmtMoney(p.equity) }}</td><td :class="pnlClass(p.pnl)">{{ fmtMoney(p.pnl, true) }}</td><td :class="pnlClass(p.pct)">{{ fmtPct(p.pct) }}</td></tr></tbody>
              </table></div>
            </details>
            <details class="ui-help"><summary>报告口径</summary><p class="hint">总资金=现金+持仓市值。最大回撤按信号日至结束日的每日收盘总资金及初始预算计算，包含已发生费用；回撤金额与百分比各取自身最大值。最大浮盈／浮亏只统计持仓期收盘价。报告不含结束后的额外复盘行情，也不按事后最高点判定买卖对错。</p></details>
          </div>
          <details v-if="reviewMode === 'chart'" class="ui-help practice-chart-help"><summary>数据与图表说明</summary><p class="hint">数据来源 {{ review.source }} · 快照截止 {{ review.dataEndDate }}；复盘展示至相对信号日 +{{ review.reviewEndDay }}，不影响结束前已完整保留的卖后观察。B=买入、S=卖出，标记位于当日 K 线上下方，准确价格见标记提示与成交明细。MACD 参数 12/26/9，柱=DIF−DEA。</p>
            <details v-if="review.coverage" class="ui-help">
              <summary>抽题覆盖（扫描 {{ review.coverage.scanned }} 只 · 有本地数据 {{ review.coverage.withData }} 只 · 命中 {{ review.coverage.hits }} 个）</summary>
            <div class="msg">扫描股票 {{ review.coverage.codes }} 只，失败 {{ review.coverage.failed }} 只。
              <div v-for="(f, i) in review.coverage.failures || []" :key="i">{{ f.code }}：{{ f.message }}</div>
            </div>
            </details>
          </details>
          <p v-if="reviewMode === 'chart' && review.extraDays < 20" class="hint" role="status">结束后仅有 {{ review.extraDays }} 个可用交易日，不足 20 日；按开题快照实际数据展示。</p>
          <p v-if="reviewMode === 'chart' && klineWarning" class="hint" role="status">{{ klineWarning }}</p>
          <div v-show="reviewMode === 'chart'" ref="reviewChart" class="chart-kline" role="img" aria-label="复盘日K、成交量、MACD 与真实日期买卖点图"></div>
          <details class="ui-help practice-trades"><summary>成交明细（{{ (review.trades || []).length }} 笔）</summary>
          <div class="detail-wrap">
            <table>
              <thead><tr><th>交易日</th><th>日期</th><th>动作</th><th>价格（元）</th><th>数量（股）</th><th>含费金额（元）</th><th>手续费（元）</th></tr></thead>
              <tbody>
                <tr v-for="(t, i) in review.trades || []" :key="i">
                  <td>{{ practiceDayLabel(t.day) }}</td>
                  <td>{{ t.date }}</td>
                  <td>{{ practiceActionLabel(t.action) }}</td>
                  <td>{{ fmtMoney(t.price) }}</td>
                  <td>{{ t.quantity }}</td>
                  <td>{{ fmtMoney(t.amount) }}</td>
                  <td>{{ fmtMoney(t.fee) }}</td>
                </tr>
                <tr v-if="!(review.trades || []).length"><td colspan="7" class="empty">本题未产生成交</td></tr>
              </tbody>
            </table>
          </div>
          </details>
        </template>
      </section>
    </template>
  </template>

  <!-- 练习历史 -->
  <details v-if="!training" class="card practice-history" :open="!sess">
    <summary>历史单题与重练<span class="hint">（{{ history.length }}）</span></summary>
    <p v-if="histLoading" class="empty" role="status">载入历史…</p>
    <p v-else-if="histErr" class="msg err" role="alert">历史加载失败：{{ histErr }}
      <button type="button" class="secondary" @click="loadHistory">重试</button>
    </p>
    <p v-else-if="!history.length" class="empty">还没有练习记录；从上方开始第一题。</p>
    <div v-else class="detail-wrap">
      <table>
        <thead><tr><th>策略</th><th>范围</th><th>状态</th><th>结果</th><th>已实现盈亏</th><th>更新时间</th><th>操作</th></tr></thead>
        <tbody>
          <tr v-for="h in history" :key="h.id" :class="{selected: sess && sess.id === h.id}">
            <td>{{ h.strategyName }}</td>
            <td>{{ h.fromYear }}–{{ h.toYear }}</td>
            <td>{{ practicePhaseLabel(h.phase) }}{{ h.stage ? ' · ' + practiceStageLabel(h.stage) : '' }}{{ h.sourceSessionId ? ' · 重练' : '' }}</td>
            <td>{{ h.endReason ? practiceEndReasonLabel(h.endReason) : (h.failReason ? practiceFailReasonLabel(h.failReason) : '—') }}</td>
            <td :class="h.realizedPnl ? pnlClass(h.realizedPnl) : ''">{{ h.stage === 'sold' ? fmtMoney(h.realizedPnl, true) + ' 元（' + fmtPct(h.realizedPct) + '）' : '—' }}</td>
            <td>{{ fmtTime(h.updatedAt) }}</td>
            <td>
              <button type="button" class="secondary trade-kline-button" @click="openPractice(h)">{{ h.phase === 'finished' ? '查看' : '继续' }}</button>
              <button v-if="h.phase === 'finished'" type="button" class="secondary trade-kline-button" :disabled="retrying" @click="retryById(h.id)">重练</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <p v-if="retryMsg" class="msg" :class="retryMsg.ok ? 'ok' : 'err'" role="status">{{ retryMsg.text }}</p>
  </details>
</div>
  `,
};
