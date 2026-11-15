import { Api } from '../api.js';
import { stockLabel } from '../format.js';
import { mountKline, dropKline } from '../kline.js';
import { state } from '../store.js';
import { dlgMixin } from './dlgMixin.js';

export default {
  name: 'StockChartDialog',
  mixins: [dlgMixin],
  data() {
    return {code: '', stockName: '', chart: null, request: null, loading: false, empty: false, error: ''};
  },
  computed: {
    label() { return stockLabel(this.code, this.stockName ? {[this.code]: this.stockName} : state.stockNames); },
  },
  watch: {
    open(value) { if (!value) this.reset(); },
  },
  beforeUnmount() { this.reset(); },
  methods: {
    show(code, name) {
      this.code = code;
      this.stockName = name;
      this.open = true;
      this.load();
    },
    reset() {
      this.request?.abort();
      this.request = null;
      dropKline(this.chart);
      this.chart = null;
      this.loading = false;
      this.empty = false;
      this.error = '';
    },
    async load() {
      this.reset();
      this.loading = true;
      const request = new AbortController();
      this.request = request;
      try {
        const rows = await Api.kline(this.code, '', '', request.signal);
        await this.$nextTick();
        if (this.request !== request || !this.$refs.dlg?.open) return;
        if (!rows.length) {
          this.empty = true;
          return;
        }
        this.chart = mountKline(this.$refs.chart);
        if (!this.chart) throw new Error('图表组件未加载，请刷新页面后重试');
        this.chart.setData({rows, identity: this.code, title: this.label});
      } catch (error) {
        if (this.request !== request) return;
        dropKline(this.chart);
        this.chart = null;
        this.error = error.message;
      } finally {
        if (this.request === request) {
          this.request = null;
          this.loading = false;
        }
      }
    },
    retry() {
      this.$refs.close.focus();
      this.load();
    },
  },
  template: `
<teleport to="body">
  <dialog ref="dlg" id="stock-chart-dialog" class="stock-chart-dialog" aria-labelledby="stock-chart-title">
    <div class="dlg-head">
      <h2 id="stock-chart-title">{{ label }} · 日K线</h2>
      <button ref="close" type="button" class="secondary" autofocus @click="closeDlg">关闭</button>
    </div>
    <div class="stock-chart-frame">
      <div ref="chart" class="chart-kline" aria-label="日K、成交量、MACD 图" :aria-busy="String(loading)"></div>
      <div v-if="loading || empty || error" class="stock-chart-status">
        <p v-if="loading" class="hint" role="status">正在加载日K线…</p>
        <p v-else-if="empty" class="hint" role="status">该标的暂无日K数据。</p>
        <p v-else class="builder-field-error" role="alert">{{ error }}</p>
        <button v-if="error" type="button" class="secondary" @click="retry">重试</button>
      </div>
    </div>
  </dialog>
</teleport>
  `,
};
