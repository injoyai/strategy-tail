import Tab7Practice from './Tab7Practice.js';
import { state } from '../store.js';
import { Api } from '../api.js';
import { newCommandId, fmtMoney, fmtPct, fmtTime, pnlClass, practiceFailReasonLabel } from '../practice.js';
import { trainingMetrics, trainingResultLabel, trainingReportText } from '../training.js';

export default {
  name: 'Tab7Training',
  components: {Tab7Practice},
  inject: ['startTask'],
  setup() { return {state, fmtMoney, fmtPct, fmtTime, pnlClass, practiceFailReasonLabel, trainingMetrics, trainingResultLabel}; },
  data() { return {
    banks: [], trainings: [], catalogLoading: false, catalogErr: '', catalogSeq: 0,
    pane: 'banks', bank: null, train: null, report: null, drill: null, legacyId: '',
    strategyId: '', fromYear: 2020, toYear: new Date().getFullYear(),
    seq: 0, loading: false, busy: false, error: '', message: '',
    bankRequest: null, startRequest: null, pending: null, confirmEnd: false,
    timer: null, polling: false, reportLoading: false, reportErr: '',
  }; },
  computed: {
    currentYear() { return new Date().getFullYear(); },
    questionTraining() { return this.train ? {...this.train, ...(this.drill ? {session: this.drill, readOnly: true} : {})} : null; },
  },
  async mounted() {
    const p = new URLSearchParams(location.search);
    this.loadCatalog();
    try { if (!state.builderStrategies.length) state.builderStrategies = (await Api.builderStrategies()).strategies || []; }
    catch (e) { this.error = '策略载入失败：' + e.message; }
    if (p.get('t')) this.openTraining(p.get('t'));
    else if (p.get('bank')) this.openBank(p.get('bank'));
    else if (p.get('p')) { this.pane = 'legacy'; this.legacyId = p.get('p'); state.practiceCurrentId = this.legacyId; }
  },
  unmounted() { this.seq++; this.catalogSeq++; clearInterval(this.timer); },
  methods: {
    url(kind, id) {
      state.practiceTrainingId = kind === 't' ? id : '';
      state.practiceBankId = kind === 'bank' ? id : '';
      if (kind !== 'p') state.practiceCurrentId = '';
      if (state.activeTab !== 7) return;
      const p = new URLSearchParams(location.search);
      for (const k of ['p', 't', 'bank']) p.delete(k);
      p.set('tab', 'practice'); if (kind && id) p.set(kind, id);
      history.replaceState(null, '', '?' + p.toString());
    },
    home(pane = 'banks') {
      this.seq++; clearInterval(this.timer); this.timer = null;
      this.pane = pane; this.bank = null; this.train = null; this.report = null; this.drill = null;
      this.loading = false; this.reportLoading = false; this.error = ''; this.message = ''; this.pending = null; this.confirmEnd = false;
      this.legacyId = ''; this.url(); this.loadCatalog();
    },
    async loadCatalog() {
      const seq = ++this.catalogSeq; this.catalogLoading = true; this.catalogErr = '';
      const results = await Promise.allSettled([Api.practiceBanks(), Api.trainings()]);
      if (seq !== this.catalogSeq) return;
      if (results[0].status === 'fulfilled') this.banks = results[0].value.banks || [];
      if (results[1].status === 'fulfilled') this.trainings = results[1].value.trainings || [];
      this.catalogErr = results.filter(x => x.status === 'rejected').map(x => x.reason.message).join('；'); this.catalogLoading = false;
    },
    async createBank() {
      if (this.busy) return;
      const st = state.builderStrategies.find(x => x.id === this.strategyId);
      if (!st) { this.error = '请选择已保存策略'; return; }
      const params = {strategyId: st.id, strategyVersion: st.version, fromYear: Number(this.fromYear), toYear: Number(this.toYear)};
      if (!this.bankRequest || JSON.stringify(this.bankRequest.params) !== JSON.stringify(params)) this.bankRequest = {params, id: newCommandId()};
      const seq = ++this.seq; this.busy = true; this.error = '';
      try {
        const b = await Api.createPracticeBank({...params, requestId: this.bankRequest.id});
        this.bankRequest = null; if (seq !== this.seq) return;
        this.setBank(b); this.loadCatalog();
      } catch (e) { if (seq === this.seq) this.error = e.status == null ? '建库请求尚未确认，可直接重试，不会重复建库：' + e.message : e.message; }
      finally { this.busy = false; }
    },
    setBank(b) {
      this.bank = b; this.train = null; this.report = null; this.pane = 'banks'; this.url('bank', b.id);
      clearInterval(this.timer); this.timer = null;
      if (b.phase === 'preparing') {
        this.startTask('practice_prepare', {phase: 'prepare', runId: b.id});
        this.timer = setInterval(() => this.pollBank(), 2000);
      }
    },
    async openBank(id) {
      const seq = ++this.seq; this.loading = true; this.error = '';
      try { const b = await Api.practiceBank(id); if (seq === this.seq) this.setBank(b); }
      catch (e) { if (seq === this.seq) this.error = e.message; }
      finally { if (seq === this.seq) this.loading = false; }
    },
    async pollBank() {
      if (this.polling || !this.bank || this.bank.phase !== 'preparing') return;
      const id = this.bank.id, seq = this.seq; this.polling = true;
      try {
        const b = await Api.practiceBank(id);
        if (seq !== this.seq || this.bank?.id !== id) return;
        this.bank = b; this.error = '';
        if (b.phase !== 'preparing') { clearInterval(this.timer); this.timer = null; this.loadCatalog(); }
      } catch (e) { if (seq === this.seq) this.error = '题库状态查询失败，将继续重试：' + e.message; }
      finally { this.polling = false; }
    },
    practiceOnTaskDone(id) {
      if (this.bank?.id === id) this.pollBank();
      else { this.loadCatalog(); this.$refs.legacy?.practiceOnTaskDone(id); }
    },
    async startTraining(bankId) {
      if (this.busy) return;
      if (this.startRequest?.bankId !== bankId) this.startRequest = {bankId, requestId: newCommandId()};
      const seq = ++this.seq; this.busy = true; this.error = '';
      try {
        const t = await Api.startTraining(this.startRequest); this.startRequest = null;
        if (seq === this.seq) { this.train = null; this.acceptTraining(t); this.loadCatalog(); }
      } catch (e) { if (seq === this.seq) this.error = e.status == null ? '开始训练结果尚未确认，可重试同一请求：' + e.message : e.message; }
      finally { this.busy = false; }
    },
    async openTraining(id) {
      const seq = ++this.seq; this.loading = true; this.error = ''; this.report = null; this.drill = null;
      try { const t = await Api.training(id); if (seq === this.seq) { this.train = null; this.acceptTraining(t); } }
      catch (e) { if (seq === this.seq) this.error = e.message; }
      finally { if (seq === this.seq) this.loading = false; }
    },
    acceptTraining(t) {
      if (this.train && (this.train.id !== t.id || this.train.revision > t.revision)) return;
      this.train = t; this.bank = null; this.pane = 'training'; this.confirmEnd = false; this.drill = null;
      clearInterval(this.timer); this.timer = null; this.url('t', t.id);
      if (t.phase === 'finished') this.loadReport();
    },
    async trainingAction(action) {
      if (!this.train || this.train.phase !== 'active' || this.busy) return;
      if (action === 'end_training' && !this.confirmEnd) { this.confirmEnd = true; return; }
      const seq = this.seq, id = this.train.id;
      const cmd = this.pending?.action === action ? this.pending : {action, commandId: newCommandId(), expectedRevision: this.train.revision, questionId: this.train.session.id};
      this.pending = cmd; this.busy = true; this.error = '';
      try {
        const t = await Api.trainingAction(id, cmd);
        if (seq !== this.seq || this.train?.id !== id) return;
        this.pending = null; this.acceptTraining(t); this.loadCatalog();
      } catch (e) {
        if (seq !== this.seq || this.train?.id !== id) return;
        if (e.status != null) this.pending = null;
        this.error = e.status == null ? '操作结果尚未确认，请重试；不会重复抽题或结束：' + e.message : e.message;
        if (e.status === 409) await this.openTraining(id);
      } finally { this.busy = false; }
    },
    async loadReport() {
      if (!this.train || this.train.phase !== 'finished') return;
      const seq = this.seq, id = this.train.id; this.reportLoading = true; this.reportErr = '';
      try { const r = await Api.trainingReport(id); if (seq === this.seq && this.train?.id === id) this.report = r; }
      catch (e) { if (seq === this.seq) this.reportErr = e.message; }
      finally { if (seq === this.seq) this.reportLoading = false; }
    },
    async reviewQuestion(id) {
      const seq = this.seq, trainingId = this.train.id; this.loading = true; this.error = '';
      try {
        const s = await Api.trainingQuestion(trainingId, id);
        if (seq !== this.seq || this.train?.id !== trainingId) return;
        this.drill = s; await this.$nextTick(); this.$refs.question?.openReview();
      } catch (e) { if (seq === this.seq) this.error = e.message; }
      finally { if (seq === this.seq) this.loading = false; }
    },
    onRetake(s) {
      this.home('legacy'); this.legacyId = s.id; this.message = '同题重练单独保存，不计入正式训练报告。'; this.url('p', s.id);
    },
    downloadReport() {
      if (!this.report) return;
      const url = URL.createObjectURL(new Blob([trainingReportText(this.report)], {type: 'text/plain;charset=utf-8'}));
      const link = document.createElement('a'); link.href = url; link.download = `训练分析报告-${this.report.id}.txt`; link.click();
      setTimeout(() => URL.revokeObjectURL(url), 0);
    },
  },
  template: `
<div class="training-layout">
  <div class="filters training-nav" aria-label="练习导航">
    <button type="button" class="secondary" @click="home('banks')">题库</button>
    <button type="button" class="secondary" @click="home('history')">训练历史<span v-if="trainings.length">（{{ trainings.length }}）</span></button>
    <button type="button" class="secondary" @click="home('legacy')">历史单题 / 重练</button>
  </div>
  <p v-if="error" class="msg err" role="alert">{{ error }}</p>
  <p v-if="message" class="hint" role="status">{{ message }}</p>
  <p v-if="loading" class="empty" role="status">载入中…</p>

  <template v-if="pane === 'banks' && !train">
    <section v-if="!bank" class="card training-bank-form">
      <h3>建立题库</h3>
      <div class="filters">
        <label>策略 <select v-model="strategyId" :disabled="busy"><option disabled value="">选择已保存策略</option><option v-for="s in state.builderStrategies" :value="s.id" :key="s.id">{{ s.name }}（v{{ s.version }}）</option></select></label>
        <label>年份 <input type="number" v-model="fromYear" min="1990" :max="currentYear" :disabled="busy" aria-label="题库起始年份"></label>
        <span>至</span><input type="number" v-model="toYear" min="1990" :max="currentYear" :disabled="busy" aria-label="题库结束年份">
        <button type="button" class="primary" :disabled="busy || !strategyId" @click="createBank">{{ busy ? '提交中…' : '建立题库' }}</button>
      </div>
      <p class="hint">首次扫描保存全部匹配机会；以后训练直接复用。策略和行情固定，每题独立 2 万元，训练内随机且不重复。</p>
    </section>
    <section v-if="bank" class="card">
      <h3>{{ bank.strategyName }} · {{ bank.fromYear }}–{{ bank.toYear }} · v{{ bank.strategyVersion }}</h3>
      <template v-if="bank.phase === 'preparing'">
        <p role="status">正在建立题库，扫描进度见任务栏。可以离开本页，刷新后继续查看；加载与保存阶段不估算剩余时间。</p>
        <button type="button" class="secondary" @click="pollBank">刷新状态</button>
      </template>
      <template v-else-if="bank.phase === 'ready'">
        <p class="hint">{{ bank.total }} 题 · 行情最晚截止 {{ bank.dataEndDate }} · 建库 {{ fmtTime(bank.readyAt) }}</p>
        <p v-if="bank.coverage" class="hint">{{ bank.coverage.codes }} 只股票 · 有数据 {{ bank.coverage.withData }} · 成功求值 {{ bank.coverage.succeeded }} · 失败 {{ bank.coverage.failed }}。匹配仅包含成功读取、求值的范围；各股截止时间可能不同。</p>
        <button type="button" class="primary" :disabled="busy" @click="startTraining(bank.id)">{{ busy ? '开始中…' : '开始新训练' }}</button>
      </template>
      <p v-else class="msg err" role="status">{{ bank.phase === 'cancelled' ? '已停止建库，未发布题库；可返回重新建立。' : practiceFailReasonLabel(bank.failReason) }}</p>
    </section>
    <section v-if="!bank" class="card">
      <h3>可复用题库</h3>
      <p v-if="catalogLoading" role="status">载入题库…</p>
      <p v-if="catalogErr" class="msg err" role="alert">{{ catalogErr }} <button type="button" class="secondary" @click="loadCatalog">重试</button></p>
      <p v-if="!catalogLoading && !banks.length" class="empty">还没有题库，请先选择策略建立。</p>
      <div v-if="banks.length" class="detail-wrap"><table>
        <thead><tr><th>策略</th><th>范围 / 版本</th><th>题数 / 状态</th><th>建立时间</th><th>操作</th></tr></thead>
        <tbody><tr v-for="b in banks" :key="b.id"><td>{{ b.strategyName }}</td><td>{{ b.fromYear }}–{{ b.toYear }} / v{{ b.strategyVersion }}</td><td>{{ b.phase === 'ready' ? b.total + ' 题' : b.phase === 'preparing' ? '建立中' : b.phase === 'cancelled' ? '已停止' : '建立失败' }}</td><td>{{ fmtTime(b.createdAt) }}</td><td><button type="button" class="secondary" :disabled="busy" @click="openBank(b.id)">查看</button> <button v-if="b.phase === 'ready'" type="button" class="primary" :disabled="busy" @click="startTraining(b.id)">开始训练</button></td></tr></tbody>
      </table></div>
      <div v-if="trainings.some(t => t.phase === 'active')" class="filters training-resume">
        <span class="hint">未结束训练：</span><button v-for="t in trainings.filter(x => x.phase === 'active')" :key="t.id" type="button" class="secondary" @click="openTraining(t.id)">继续 {{ fmtTime(t.startedAt) }} · 已练 {{ t.practiced }} 题</button>
      </div>
    </section>
  </template>

  <template v-if="pane === 'training' && train">
    <section class="card training-toolbar">
      <div class="practice-stats" role="group" aria-label="训练状态">
        <span>训练<b>{{ train.phase === 'finished' ? '已结束' : '进行中' }}</b></span><span>当前<b>第 {{ train.practiced }} 题 / 题库 {{ train.total }} 题</b></span><span>开始<b>{{ fmtTime(train.startedAt) }}</b></span>
      </div>
      <div class="filters">
        <button v-if="train.phase === 'active' && train.session.phase === 'finished'" type="button" class="primary" :disabled="busy" @click="trainingAction('next_question')">{{ busy ? '处理中…' : '开始下一题' }}</button>
        <button v-if="train.phase === 'active'" type="button" class="danger" :disabled="busy" @click="trainingAction('end_training')">{{ confirmEnd ? '再次点击确认结束训练' : '结束训练并生成报告' }}</button>
        <button v-if="confirmEnd" type="button" class="secondary" @click="confirmEnd = false">继续训练</button>
        <button v-if="pending && !busy" type="button" class="secondary" @click="trainingAction(pending.action)">重试未确认操作</button>
        <button v-if="train.phase === 'finished'" type="button" class="secondary" :disabled="!report" @click="downloadReport">下载训练报告</button>
        <button v-if="drill" type="button" class="secondary" @click="drill = null">返回训练报告</button>
      </div>
      <p v-if="confirmEnd" class="hint">结束后成绩冻结。当前持仓按已揭示收盘价估值，单独列为未平仓，不自动卖出。</p>
    </section>
    <template v-if="train.phase === 'finished' && !drill">
      <section class="card training-report">
        <h3>训练分析报告</h3>
        <p v-if="reportLoading" role="status">载入报告…</p>
        <p v-if="reportErr" class="msg err" role="alert">{{ reportErr }} <button type="button" class="secondary" @click="loadReport">重试</button></p>
        <template v-if="report">
          <p class="hint">{{ report.strategyName }} v{{ report.strategyVersion }} · {{ report.fromYear }}–{{ report.toYear }} · 结束 {{ fmtTime(report.endedAt) }}</p>
          <div class="practice-stats training-counts"><span>练习<b>{{ report.practiced }} 题</b></span><span>已平仓<b>{{ report.closed }}</b></span><span>未交易<b>{{ report.untraded }}</b></span><span>未平仓<b>{{ report.open }}</b></span><span>盈利 / 亏损 / 打平<b>{{ report.wins }} / {{ report.losses }} / {{ report.breakeven }}</b></span></div>
          <dl class="training-metrics"><template v-for="m in trainingMetrics(report)" :key="m.label"><div><dt>{{ m.label }}</dt><dd :class="pnlClass(m.pnl)">{{ m.value }}</dd></div></template></dl>
          <p class="hint">各题独立资金；胜率、平均收益率只含真实已平仓题，打平计入分母。实际费用含未平仓买入费，盈亏已扣费，无需再次扣除。同题重练、历史单题及其他训练不计入。</p>
          <div class="detail-wrap"><table>
            <thead><tr><th>题目</th><th>信号 / 结束</th><th>结果</th><th>买入支出 / 卖出收入（元）</th><th>已实现（元）</th><th>浮动（元）</th><th>收益率</th><th>费用（元）</th><th>持仓日</th><th>操作</th></tr></thead>
            <tbody><tr v-for="(q,i) in report.questions" :key="q.id"><td>{{ i + 1 }} · {{ q.code }}</td><td>{{ q.signalDate }} / {{ q.endDate }}</td><td>{{ trainingResultLabel(q.status) }}</td><td>{{ fmtMoney(q.buyCost) }} / {{ fmtMoney(q.sellIncome) }}</td><td :class="pnlClass(q.realizedPnl)">{{ q.status === 'sold' ? fmtMoney(q.realizedPnl,true) : '—' }}</td><td :class="pnlClass(q.openPnl)">{{ q.status === 'open' ? fmtMoney(q.openPnl,true) : '—' }}</td><td :class="pnlClass(q.returnPct)">{{ q.returnPct == null ? '不适用' : fmtPct(q.returnPct) }}</td><td>{{ fmtMoney(q.fees) }}</td><td>{{ q.status === 'untraded' ? '—' : q.holdingDays }}</td><td><button type="button" class="secondary" :disabled="loading" @click="reviewQuestion(q.id)">单题复盘 / 重练</button></td></tr></tbody>
          </table></div>
          <details v-if="report.coverage" class="ui-help"><summary>扫描覆盖与统计边界</summary><p class="hint">题库共 {{ report.bankTotal }} 题，仅统计本训练实际练过的 {{ report.practiced }} 题。扫描 {{ report.coverage.codes }} 只，有行情 {{ report.coverage.withData }} 只，求值失败 {{ report.coverage.failed }} 只，命中 {{ report.coverage.hits }} 个。复盘额外行情不改变成绩。</p><ul><li v-for="(f,i) in report.coverage.failures || []" :key="i">{{ f.code }}：{{ f.message }}</li></ul></details>
        </template>
      </section>
    </template>
    <Tab7Practice v-if="train.phase === 'active' || drill" ref="question" :key="questionTraining.session.id" :training="questionTraining" @training-update="acceptTraining" @retake="onRetake" @home="home()" />
  </template>

  <section v-if="pane === 'history'" class="card">
    <h3>训练历史</h3>
    <p v-if="catalogLoading" role="status">载入中…</p><p v-if="catalogErr" class="msg err" role="alert">{{ catalogErr }} <button type="button" class="secondary" @click="loadCatalog">重试</button></p>
    <p v-if="!catalogLoading && !trainings.length" class="empty">还没有训练记录；历史单题单独保留。</p>
    <div v-if="trainings.length" class="detail-wrap"><table><thead><tr><th>开始</th><th>结束</th><th>题库</th><th>已练题数</th><th>状态</th><th>操作</th></tr></thead><tbody><tr v-for="t in trainings" :key="t.id"><td>{{ fmtTime(t.startedAt) }}</td><td>{{ t.endedAt ? fmtTime(t.endedAt) : '—' }}</td><td>{{ (banks.find(b => b.id === t.bankId) || {}).strategyName || t.bankId }}<small class="hint">{{ t.bankId }}</small></td><td>{{ t.practiced }}</td><td>{{ t.phase === 'finished' ? '已结束' : '进行中' }}</td><td><button type="button" class="secondary" @click="openTraining(t.id)">{{ t.phase === 'finished' ? '查看报告' : '继续训练' }}</button></td></tr></tbody></table></div>
  </section>
  <template v-if="pane === 'legacy'"><p class="hint">历史单题没有训练归属；同题重练单独记录，均不纳入正式训练统计。</p><Tab7Practice ref="legacy" :key="legacyId" :initial-session-id="legacyId" history-only /></template>
</div>
`,
};
