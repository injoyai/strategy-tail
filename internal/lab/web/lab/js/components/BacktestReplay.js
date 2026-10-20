// BacktestReplay — 03 逐日回放视图（回放区替换结果区）。
// 状态与状态转换全部在 replay.js（存 store.replay，跨页保留）；本组件只负责
// 加载、计时器与图表渲染。图表实例与计时器为非响应式组件字段。
// 未来信息截断（R03）由 replay.js 派生保证；请求失效由 seq/klineSeq 保证（R10）。
import { watch } from '../vendor/vue.esm-browser.prod.js';
import { state } from '../store.js';
import { Api } from '../api.js';
import { mountChart, dropChart, NAV_COLORS } from '../echarts.js';
import { mountKline, dropKline } from '../kline.js';
import * as R from '../replay.js';

export default {
  name: 'BacktestReplay',
  setup() {
    return { state };
  },
  data() {
    return { equityC: null, klineC: null, timer: null, equityError: '' };
  },
  computed: {
    s() { return state.replay; },
    rep() { return state.currentReport; },
    reportId() { return this.rep?.id ?? ''; },
    variants() { return this.rep?.variants ?? []; },
    // 变体下拉只列报告全部变体；无回放数据的项禁用（服务端会拒绝）。
    replayVariants() {
      return this.variants.map((v, i) => ({index: i, name: v.name, available: !!v.replay?.available}));
    },
    variantName() {
      return this.s?.data?.variantName ?? this.variants[this.s?.variantIdx]?.name ?? '';
    },
    dates() { return this.s?.data?.dates ?? []; },
    curDate() { return this.dates[this.s?.dateIdx] ?? ''; },
    day() { return this.s?.data?.days?.[this.s?.dateIdx] ?? null; },
    holdings() { return this.day?.holdings ?? []; },
    trades() { return this.day?.trades ?? []; },
    rules() { return this.s?.data?.rules ?? []; },
    es() { return R.equitySeries(this.s); },
    totalReturn() { return this.es.values[this.s?.dateIdx] ?? null; },
    // 上证对照（spec §4.1）：按回放日期轴对齐，未来日期为 null 空白。
    benchmarkValues() {
      const bench = this.rep?.benchmark;
      if (!bench?.length || !this.dates.length) return [];
      const map = new Map(bench.map(p => [p.date, p.return]));
      return this.dates.map((d, i) => (i <= this.s.dateIdx && map.has(d)) ? map.get(d) : null);
    },
    // 个股下拉：截至当日买过的代码 ∪ 当日持仓（已卖出可回看，R09）。
    stockCodes() {
      const seen = new Set();
      const out = [];
      for (const d of R.visibleDays(this.s)) {
        for (const t of d.trades ?? []) {
          if (!seen.has(t.code)) { seen.add(t.code); out.push(t.code); }
        }
      }
      for (const h of this.holdings) {
        if (!seen.has(h.code)) { seen.add(h.code); out.push(h.code); }
      }
      return out;
    },
    selectedTrades() { return this.s?.code ? R.visibleTradesFor(this.s, this.s.code) : []; },
    speedOptions() {
      const label = {slow: '慢速', mid: '中速', fast: '快速'};
      return Object.entries(R.SPEEDS).map(([k, ms]) => ({k, ms, label: label[k] ?? k}));
    },
    progressText() { return this.dates.length ? `${this.s.dateIdx + 1}/${this.dates.length}` : '—'; },
    atEnd() { return !this.dates.length || this.s.dateIdx >= this.dates.length - 1; },
  },
  mounted() {
    if (!state.replay) state.replay = R.createSession();
    watch(() => state.activeTab, n => {
      if (n !== 2) {
        clearTimeout(this.timer);
        R.pause(state.replay);
      }
    });
    // 播放驱动统一由 playing 状态承接：手动播放/自动恢复都走同一路径。
    watch(() => state.replay?.playing, playing => {
      clearTimeout(this.timer);
      if (playing) this.schedule();
    });
    // 日期或数据变化 → 重绘（未来截断随 dateIdx 变化）。
    watch(() => [state.replay?.dateIdx, state.replay?.data], () => {
      this.renderEquity();
      this.renderKline();
    }, {flush: 'post'});
    this.$nextTick(() => {
      this.renderEquity();
      this.renderKline();
    });
    const s = state.replay;
    if (s && !s.data && !s.loading && !s.error) this.load();
  },
  unmounted() {
    clearTimeout(this.timer);
    // 离开回放区域（含切页销毁）即暂停；会话留在 store，重挂载从暂停态恢复。
    R.pause(state.replay);
    dropChart(this.equityC);
    dropKline(this.klineC);
  },
  methods: {
    fmtMoney(v) {
      return v == null || !isFinite(v) ? '-' : Math.round(v).toLocaleString('zh-CN');
    },
    fmtPnL(v) {
      return v == null || !Number.isFinite(v) ? '-' : (v > 0 ? '+' : '') + v.toLocaleString('zh-CN', {minimumFractionDigits: 2, maximumFractionDigits: 2});
    },
    fmtPct(v) { return v == null || !isFinite(v) ? '-' : (v >= 0 ? '+' : '') + v.toFixed(2) + '%'; },
    fmtRatio(v) { return v == null || !isFinite(v) ? '-' : v.toFixed(2) + '%'; },
    pnlCls(v) { return v == null || !isFinite(v) || v === 0 ? '' : v > 0 ? 'pos' : 'neg'; },
    fmtTime(t) { return typeof t === 'string' && t.length > 10 ? t.slice(11) : (t || '-'); },
    fmtDay(t) { return typeof t === 'string' && t.length >= 10 ? t.slice(0, 10) : (t || '-'); },
    markHint(h) {
      return h.markDate && this.curDate && h.markDate !== this.curDate ? `（沿用${h.markDate}价）` : '';
    },
    reasonText(t) { return R.reasonLines(t, this.rules).join(' → ') || '-'; },
    onVariant(e) {
      const s = state.replay;
      if (!s) return;
      R.switchVariant(s, +e.target.value);
      if (s.error || !s.data) this.load();
    },
    onSpeed(e) {
      R.setSpeed(state.replay, e.target.value);
      // 播放中调速立即生效（重置当前间隔）。
      if (state.replay?.playing) this.schedule();
    },
    onAutoPause(e) { R.setAutoPause(state.replay, e.target.checked); },
    togglePlay() {
      const s = state.replay;
      if (!s) return;
      if (s.playing) R.pause(s);
      else R.play(s);
    },
    onSlider(e) { R.stepTo(state.replay, +e.target.value); },
    // 模板无法访问模块级 import，步进经 methods 转发。
    stepPrev() { R.stepPrev(state.replay); },
    stepNext() { R.stepNext(state.replay); },
    pickStock(code) {
      const s = state.replay;
      if (!s) return;
      R.selectStock(s, code);
      // 代码变化会清空快照；同一只重复点击不重复请求。
      if (s.code && !s.kline && !s.klineLoading) this.loadKline();
    },
    exitReplay() {
      R.pause(state.replay);
      state.replayOn = false;
    },
    schedule() {
      clearTimeout(this.timer);
      this.timer = setTimeout(() => this.tick(), R.SPEEDS[state.replay?.speed] ?? R.SPEEDS.mid);
    },
    // 单个可取消计时器：上一次更新完成（$nextTick）后才安排下一步。
    async tick() {
      const s = state.replay;
      if (!s?.playing) return;
      if (state.activeTab !== 2) { R.pause(s); return; }
      R.advance(s);
      await this.$nextTick();
      if (state.replay === s && s.playing) this.schedule();
    },
    async load() {
      const s = state.replay;
      if (!s) return;
      R.pause(s);
      clearTimeout(this.timer);
      if (!this.reportId) {
        s.loading = false;
        s.error = '当前报告缺少回放标识，请重新打开报告';
        return;
      }
      const request = ++s.seq;
      s.loading = true;
      s.error = '';
      try {
        const data = await Api.replay(this.reportId, s.variantIdx);
        if (state.replay !== s || s.seq !== request) return; // 旧响应丢弃
        s.data = data;
        // 策略切换保留日期（R10）；新变体更短时收敛到末日。
        const n = data.days?.length ?? 0;
        if (s.dateIdx >= n) s.dateIdx = Math.max(0, n - 1);
        s.loading = false;
      } catch (e) {
        if (state.replay !== s || s.seq !== request) return;
        s.loading = false;
        s.error = e?.message || String(e);
      }
    },
    async loadKline() {
      const s = state.replay;
      if (!s?.code || !this.reportId) return;
      const request = ++s.klineSeq;
      s.klineLoading = true;
      s.klineError = '';
      try {
        const rows = await Api.replayKline(this.reportId, s.code);
        if (state.replay !== s || s.klineSeq !== request) return; // 旧响应丢弃
        if (!rows?.length) throw new Error('该股票的回放行情为空，请重新回测');
        s.kline = rows;
        s.klineLoading = false;
        await this.$nextTick();
        if (state.replay === s && s.klineSeq === request) this.renderKline();
      } catch (e) {
        if (state.replay !== s || s.klineSeq !== request) return;
        s.klineLoading = false;
        s.klineError = e?.message || String(e);
      }
    },
    renderEquity() {
      const el = this.$refs.equityChart;
      const s = state.replay;
      if (!el || !s?.data) return;
      if (!this.equityC) {
        this.equityC = mountChart(el);
        this.equityError = this.equityC ? '' : '图表组件未加载，收益图暂不可用，请刷新重试；账户数值仍可查看。';
        if (!this.equityC) return;
      }
      const es = R.equitySeries(s);
      const series = [{
        name: '策略收益', type: 'line', showSymbol: false, connectNulls: false,
        lineStyle: {width: 1.5, color: NAV_COLORS.gross}, itemStyle: {color: NAV_COLORS.gross},
        data: es.values,
        markLine: {silent: true, symbol: 'none', label: {show: false},
          lineStyle: {type: 'dashed', color: 'rgba(216,180,90,.55)'},
          data: [{xAxis: s.dateIdx}]},
      }];
      if (this.benchmarkValues.some(v => v != null)) {
        series.push({name: '上证指数', type: 'line', showSymbol: false, connectNulls: false,
          lineStyle: {width: 2, type: 'dashed', color: NAV_COLORS.net}, itemStyle: {color: NAV_COLORS.net},
          data: this.benchmarkValues});
      }
      this.equityC.setOption({
        animation: false,
        backgroundColor: 'transparent',
        title: {text: '账户累计收益率', left: 10, top: 6, textStyle: {fontSize: 13, color: '#e9eef8'}},
        tooltip: {trigger: 'axis', valueFormatter: v => v == null ? '—' : `${Number(v).toFixed(2)}%`},
        legend: {top: 30, right: 10, data: ['策略收益', '上证指数']},
        grid: {left: 60, right: 20, top: 60, bottom: 36},
        xAxis: {type: 'category', data: es.dates, boundaryGap: false, axisLabel: {hideOverlap: true}},
        yAxis: {scale: true, axisLabel: {formatter: '{value}%'}},
        series,
      }, true);
    },
    renderKline() {
      const el = this.$refs.klineChart;
      const s = state.replay;
      if (!el) return;
      if (!s?.code || !s.kline) {
        if (this.klineC) this.klineC.clear();
        return;
      }
      if (!this.klineC) {
        this.klineC = mountKline(el);
        if (!this.klineC) { s.klineError = '图表组件未加载，请刷新页面后重试'; return; }
      }
      const rows = R.klineVisible(s.kline, this.curDate);
      if (!rows.length) { this.klineC.clear(); return; }
      const dates = new Set(rows.map(k => k.date));
      const markers = this.selectedTrades.filter(t => dates.has(t.date)).map(t => ({
        date: t.date, price: t.execPrice, kind: t.side,
        text: `${t.side === 'buy' ? '买入' : '卖出'} ${t.date} · 成交 ${t.execPrice.toFixed(2)}（原始 ${t.price.toFixed(2)}）`,
      }));
      try {
        this.klineC.setData({rows, markers, identity: `${this.reportId}/${s.code}`, title: `${s.code} · ${this.curDate}`});
      } catch (e) {
        s.klineError = e?.message || String(e);
        R.pause(s);
      }
    },
  },
  template: `
<div class="card">
  <h3>逐日回放 <span class="hint">{{ variantName }} · {{ progressText }}</span></h3>

  <p v-if="s.loading" class="hint" role="status">加载回放数据中…</p>
  <div v-else-if="s.error" class="warnbar" role="alert">回放数据加载失败：{{ s.error }}
    <button type="button" class="secondary" @click="load">重试</button>
    <button type="button" class="secondary" @click="exitReplay">返回汇总</button>
  </div>

  <template v-if="s.data">
    <div class="filters replay-bar">
      <select :value="s.variantIdx" @change="onVariant" aria-label="回放变体">
        <option v-for="v in replayVariants" :key="v.index" :value="v.index" :disabled="!v.available">{{ v.name }}{{ v.available ? '' : '（无回放数据）' }}</option>
      </select>
      <button type="button" class="secondary" @click="stepPrev" :disabled="!s.dateIdx" aria-label="上一交易日">上一日</button>
      <button type="button" class="primary" @click="togglePlay" :aria-pressed="s.playing" :disabled="!s.playing && (atEnd || s.loading || s.klineLoading || !!s.error || !!s.klineError)">{{ s.playing ? '暂停' : '播放' }}</button>
      <button type="button" class="secondary" @click="stepNext" :disabled="atEnd" aria-label="下一交易日">下一日</button>
      <select :value="s.speed" @change="onSpeed" aria-label="播放速度">
        <option v-for="o in speedOptions" :key="o.k" :value="o.k">{{ o.label }}</option>
      </select>
      <label class="checkbox-label"><input type="checkbox" :checked="s.autoPause" @change="onAutoPause">成交日自动暂停</label>
      <button type="button" class="secondary" @click="exitReplay">退出回放</button>
      <input type="range" class="replay-slider" min="0" :max="Math.max(0, dates.length - 1)" :value="s.dateIdx" @input="onSlider" aria-label="回放日期进度">
      <span class="hint replay-progress">{{ curDate }} · {{ progressText }}</span>
    </div>

    <div class="replay-summary" role="status">
      <span>日期 <b>{{ curDate || '-' }}</b></span>
      <span>总资产 <b>{{ fmtMoney(day?.equity) }}</b></span>
      <span>现金 <b>{{ fmtMoney(day?.cash) }}</b></span>
      <span>持仓市值 <b>{{ fmtMoney(day?.marketValue) }}</b></span>
      <span>仓位 <b>{{ fmtRatio(day?.positionRatio) }}</b></span>
      <span>当日盈亏 <b :class="pnlCls(day?.dayPnl)">{{ fmtPnL(day?.dayPnl) }}</b></span>
      <span>累计收益 <b :class="pnlCls(totalReturn)">{{ fmtPct(totalReturn) }}</b></span>
    </div>

    <p v-if="equityError" class="warnbar" role="alert">{{ equityError }}</p>
    <div ref="equityChart" class="chart-block" role="img" aria-label="策略累计收益率与上证指数对照，当前位置以竖线标注，当前日期之后的区间不展示"></div>

    <h4 class="subhead">当日成交</h4>
    <div v-if="trades.length" class="detail-wrap replay-day">
      <table>
        <thead><tr><th>#</th><th>代码</th><th>方向</th><th>成交时间</th><th>原始价</th><th>成交价</th><th>数量</th><th>费用</th><th>触发原因</th></tr></thead>
        <tbody>
          <tr v-for="t in trades" :key="t.seq" :class="{selected: s.code === t.code}" @click="pickStock(t.code)" :aria-label="'查看 ' + t.code + ' ' + (t.side === 'buy' ? '买入' : '卖出') + ' K线'">
            <td>{{ t.seq }}</td>
            <td><button type="button" class="secondary trade-kline-button" @click.stop="pickStock(t.code)" :aria-label="'查看 ' + t.code + ' 成交K线'">{{ t.code }}</button></td>
            <td :class="t.side === 'buy' ? 'pos' : 'neg'">{{ t.side === 'buy' ? '买入' : '卖出' }}</td>
            <td>{{ fmtTime(t.time) }}</td>
            <td>{{ t.price.toFixed(2) }}</td>
            <td>{{ t.execPrice.toFixed(2) }}{{ t.minuteFallback ? '（缺分钟，按日线收盘）' : '' }}</td>
            <td>{{ t.quantity }}</td>
            <td>{{ t.fee.toFixed(2) }}</td>
            <td class="replay-reason">{{ reasonText(t) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
    <p v-else class="hint" role="status">当日无成交。</p>

    <h4 class="subhead">当日持仓</h4>
    <div v-if="holdings.length" class="detail-wrap replay-day">
      <table>
        <thead><tr><th>代码</th><th>买入日期</th><th>数量</th><th>成本单价</th><th>总成本(含费)</th><th>估值价</th><th>市值</th><th>浮动盈亏</th><th>浮动%</th></tr></thead>
        <tbody>
          <tr v-for="h in holdings" :key="h.positionId" :class="{selected: s.code === h.code}" @click="pickStock(h.code)" :aria-label="'查看 ' + h.code + ' K线'">
            <td><button type="button" class="secondary trade-kline-button" @click.stop="pickStock(h.code)" :aria-label="'查看 ' + h.code + ' 持仓K线'">{{ h.code }}</button>{{ markHint(h) }}</td>
            <td>{{ fmtDay(h.buyTime) }}</td>
            <td>{{ h.quantity }}</td>
            <td>{{ h.costPrice.toFixed(2) }}</td>
            <td>{{ fmtMoney(h.totalCost) }}</td>
            <td>{{ h.markPrice.toFixed(2) }}</td>
            <td>{{ fmtMoney(h.marketValue) }}</td>
            <td :class="pnlCls(h.floatingPnl)">{{ fmtPnL(h.floatingPnl) }}</td>
            <td :class="pnlCls(h.floatingRate)">{{ fmtPct(h.floatingRate) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
    <p v-else class="hint" role="status">当日空仓 · 现金 {{ fmtMoney(day?.cash) }} · 累计收益 {{ fmtPct(totalReturn) }}</p>

    <h4 class="subhead">个股走势（含已卖出）</h4>
    <div class="filters replay-stockbar">
      <select :value="s.code" @change="pickStock($event.target.value)" aria-label="选择个股查看K线">
        <option value="">选择个股（持仓或已发生成交）</option>
        <option v-for="c in stockCodes" :key="c" :value="c">{{ c }}</option>
      </select>
    </div>
    <p v-if="s.klineLoading" class="empty" role="status">加载K线中…</p>
    <div v-else-if="s.klineError" class="warnbar" role="alert">K线加载失败：{{ s.klineError }}
      <button type="button" class="secondary" @click="loadKline">重试</button>
    </div>
    <p v-if="s.code && !s.klineLoading && selectedTrades.length" class="hint">
      <template v-for="(t, i) in selectedTrades" :key="i">{{ t.date }} {{ t.side === 'buy' ? '买入' : '卖出' }} 原始 {{ t.price.toFixed(2) }} → 成交 {{ t.execPrice.toFixed(2) }} · {{ t.quantity }} 股 · 费用 {{ t.fee.toFixed(2) }} · 原因：{{ reasonText(t) }}{{ i < selectedTrades.length - 1 ? '；' : '' }}</template>
    </p>
    <!-- ECharts 独占容器，加载与错误提示不得放在其中。 -->
    <div v-show="s.code" :style="{visibility: s.klineLoading || s.klineError ? 'hidden' : 'visible'}" ref="klineChart" class="chart-kline" role="img" aria-label="个股日K、成交量、MACD 与买卖标记，仅展示截至当前回放日期"></div>
  </template>
</div>
  `,
};
