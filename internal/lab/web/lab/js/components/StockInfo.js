import { Api } from '../api.js';

const fields = [['pe_ttm', 'PE（TTM）'], ['pe_static', 'PE（静态）'], ['dividend_yield', '股息率'],
  ['pb_mrq', 'PB（MRQ）'], ['total_market_cap', '总市值'], ['float_market_cap', '流通市值']];
const sourceLabel = source => ({tdx: '通达信', eastmoney: '东方财富'}[source] || source);
const metricsFor = info => fields.map(([key, label]) => {
  const metric = info?.metrics?.find(m => m.key === key);
  return {...metric, key, label, text: stockMetricText(key, metric?.value)};
});
const sourcesFor = metrics => [...new Set(metrics.filter(m => Number.isFinite(m.value)).map(m => `${sourceLabel(m.source)} · ${m.date}`))];

export function stockMetricText(key, value) {
  if (!Number.isFinite(value)) return '—';
  if (key.endsWith('market_cap')) return (value / 1e8).toFixed(2) + ' 亿';
  if (key === 'dividend_yield') return value.toFixed(2) + '%';
  return value.toFixed(2) + (key.startsWith('pe_') && value < 0 ? '（亏损）' : ' 倍');
}

export const StockInfo = {
  name: 'StockInfo',
  props: {code: {type: String, required: true}, asOf: {type: String, default: ''},
    historyDate: {type: String, default: ''}, historyLabel: {type: String, default: '默认日期'}},
  data() { return {info: null, historicalInfo: null, selectedDate: '', loading: false, error: '', historicalError: '', request: 0}; },
  computed: {
    comparison() { return !!this.historyDate && !this.asOf; },
    maxHistoryDate() {
      const local = new Date(Date.now() + 8 * 3600000);
      if (local.getUTCHours() < 15) local.setUTCDate(local.getUTCDate() - 1);
      return local.toISOString().slice(0,10);
    },
    historicalAsOf() {
      const date = this.selectedDate;
      return /^\d{4}-\d{2}-\d{2}$/.test(date) && Number.isFinite(Date.parse(date)) &&
        new Date(date).toISOString().slice(0,10) === date && date <= this.maxHistoryDate ? date + 'T15:00:00+08:00' : '';
    },
    requestKey() { return this.code + '/' + this.asOf + '/' + (this.comparison ? this.historicalAsOf : ''); },
    metrics() { return metricsFor(this.info); },
    historicalMetrics() { return metricsFor(this.historicalInfo); },
    sources() { return sourcesFor(this.metrics); },
    historicalSources() { return sourcesFor(this.historicalMetrics); },
    unverified() { return [...this.metrics, ...this.historicalMetrics].some(m => m.pitStatus === 'unverified_revision_history'); },
  },
  watch: {
    historyDate: {immediate: true, handler(date) { this.selectedDate = date; }},
    code() { this.selectedDate = this.historyDate; },
    requestKey: {immediate: true, handler: 'load'},
  },
  beforeUnmount() { this.request++; },
  methods: {
    async load() {
      const request = ++this.request;
      this.info = null; this.historicalInfo = null; this.error = ''; this.historicalError = ''; this.loading = !!this.code;
      if (!this.code) return;
      const queries = [{target: 'info', error: 'error', cutoff: this.asOf}];
      if (this.comparison && this.historicalAsOf) queries.push({target: 'historicalInfo', error: 'historicalError', cutoff: this.historicalAsOf});
      await Promise.all(queries.map(async query => {
        try {
          const info = await Api.stockInfo(this.code, query.cutoff);
          if (request !== this.request) return;
          if (info.code !== this.code) throw new Error('估值数据与当前股票不符，请重试');
          this[query.target] = info;
        } catch (e) {
          if (request === this.request) this[query.error] = e.message || '读取估值失败';
        }
      }));
      if (request === this.request) this.loading = false;
    },
  },
  template: `
<section class="stock-info" aria-label="股票估值信息" :aria-busy="loading">
  <template v-if="comparison">
    <label class="stock-info-date">历史估值日期 <input type="date" v-model="selectedDate" :max="maxHistoryDate" :aria-invalid="!historicalAsOf">
      <button v-if="selectedDate !== historyDate" type="button" class="secondary" @click="selectedDate = historyDate">恢复{{ historyLabel }}</button>
    </label>
    <p v-if="!historicalAsOf" class="hint" role="alert">请选择已收盘的有效日期。</p>
    <table class="stock-info-comparison" aria-label="历史与最新估值对比">
      <thead><tr><th scope="col">指标</th><th scope="col">历史 · {{ selectedDate }}<small>截至当日 15:00 可得</small></th><th scope="col">最新可用<small>不代表历史交易日</small></th></tr></thead>
      <tbody><tr v-for="(m,i) in metrics" :key="m.key"><th scope="row">{{ m.label }}</th><td class="stock-info-history">{{ historicalMetrics[i].text }}</td><td class="stock-info-latest">{{ m.text }}</td></tr></tbody>
      <tfoot><tr><th scope="row">来源 / 日期</th><td>{{ historicalSources.join('；') || '暂无记录' }}</td><td>{{ sources.join('；') || '暂无记录' }}</td></tr></tfoot>
    </table>
    <p v-if="!loading && !historicalError && historicalAsOf && !historicalSources.length" class="hint" role="status">该历史时点暂无本地可用记录，未用最新估值回填。</p>
    <p v-if="historicalError" class="hint" role="alert">历史估值读取失败：{{ historicalError }} <button type="button" class="secondary" @click="load">重试历史估值</button></p>
  </template>
  <p v-else class="hint stock-info-caption">{{ asOf ? '截至 ' + asOf.slice(0,10) + ' 收盘时可用估值' : '最新可用估值 · 不代表图中交易日估值' }}</p>
  <dl v-if="!comparison" class="stock-info-metrics"><div v-for="m in metrics" :key="m.key">
    <dt>{{ m.label }}</dt><dd :title="m.source ? m.date + ' · ' + m.source + ' · 可得时间 ' + m.availableAt : undefined">{{ m.text }}</dd>
  </div></dl>
  <p v-if="loading" class="hint" role="status">读取估值中…</p>
  <p v-if="error" class="hint" role="alert">估值读取失败：{{ error }} <button type="button" class="secondary" @click="load">重试估值</button></p>
  <p v-else-if="!loading && !sources.length" class="hint" role="status">{{ asOf ? '该时点暂无可用的本地估值记录。' : '暂无本地估值记录，可在更新行情后重试。' }} <button type="button" class="secondary" @click="load">重试估值</button></p>
  <p v-else-if="!comparison" class="hint">{{ sources.join('；') }}</p>
  <details class="ui-help"><summary>估值口径</summary><p class="hint">PE 为市盈率，PB 为市净率；负 PE 表示亏损。股息率沿用通达信口径，缺失值显示“—”，不按零处理。市值单位为亿元。{{ asOf ? '仅读取截至回放日收盘已可得的记录。' : comparison ? '历史列按所选日收盘的可得记录读取；最新列是当前本地快照，不回填历史缺失。' : '以上是本地最新可用快照，与历史交易日可能不同。' }}{{ unverified ? '东方财富历史修订记录未验证。' : '' }}</p></details>
</section>`,
};
