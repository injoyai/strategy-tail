import { Api } from '../api.js';
import { mountKline, dropKline } from '../kline.js';
import { StockInfo } from './StockInfo.js';
import { dlgMixin } from './dlgMixin.js';

const RuleTree = {
  name: 'ScreenRuleTree',
  props: {node: {type: Object, required: true}},
  template: `
<li>
  <span :class="node.matched ? 'pos' : ''">{{ node.matched ? '通过' : '未通过' }}</span>
  {{ node.name }}
  <ul v-if="node.children?.length"><screen-rule-tree v-for="(child, index) in node.children" :key="index" :node="child" /></ul>
</li>`,
};

export default {
  name: 'ScreenDiagnosis',
  mixins: [dlgMixin],
  components: {StockInfo, ScreenRuleTree: RuleTree},
  props: {strategies: {type: Array, required: true}},
  data() {
    return {code: '', strategy: '', entry: '', data: null, chart: null, loading: false, error: '', warning: '', seq: 0};
  },
  watch: {
    open(value) {
      if (!value) this.clear();
    },
  },
  beforeUnmount() { this.clear(); },
  methods: {
    show(code, strategy = '', entry = '') {
      this.code = code;
      this.strategy = this.strategies.some(item => item.id === strategy) ? strategy : '';
      this.entry = entry;
      this.open = true;
      this.load();
    },
    clear() {
      this.seq++;
      dropKline(this.chart);
      this.chart = null;
      this.data = null;
    },
    async load() {
      this.clear();
      const seq = this.seq;
      this.loading = true;
      this.error = '';
      this.warning = '';
      try {
        const data = await Api.screenDiagnose(this.code, this.strategy);
        if (seq !== this.seq || !this.open) return;
        this.data = data;
        this.loading = false;
        await this.$nextTick();
        if (seq !== this.seq || !this.open) return;
        this.renderChart(data);
      } catch (error) {
        if (seq === this.seq) {
          this.loading = false;
          this.error = error.message;
        }
      }
    },
    renderChart(data) {
      this.chart = mountKline(this.$refs.chart);
      if (!this.chart) throw new Error('图表组件未加载，请刷新后重试');
      const markers = [];
      const mark = (time, price, kind, label, text) => markers.push({date: time.slice(0,10), price, kind, label, text});
      for (const row of data.signals) {
        const strategy = this.strategies.find(item => item.id === row.strategy)?.name || row.strategy;
        mark(row.buyTime, row.buyPrice, 'buy', '买', `${strategy} · 买点 ${row.buyTime} · ${row.buyPrice.toFixed(2)} 元`);
        if (row.sellTime) mark(row.sellTime, row.sellPrice, 'sell', '卖', `${strategy} · 卖点 ${row.sellTime} · ${row.sellPrice.toFixed(2)} 元`);
      }
      for (const annotation of data.annotations || []) {
        mark(annotation.time, annotation.price, 'signal', annotation.label, annotation.note);
      }
      const day = this.entry.slice(0, 10);
      const anchor = day ? data.klines.findIndex(row => row.date === day) : -1;
      const view = anchor < 0 ? undefined : {
        from: data.klines[Math.max(0, anchor - 30)].date,
        to: data.klines[Math.min(data.klines.length - 1, anchor + 20)].date,
      };
      const missing = this.chart.setData({rows: data.klines, markers, view, identity: data.code + '/' + day,
        title: [data.code, data.name].filter(Boolean).join(' ')});
      if (missing.length) this.warning = `${missing.length} 个标记缺少对应日K，已保留原信号记录。`;
    },
  },
  template: `
<teleport to="body">
  <dialog ref="dlg" id="screen-diagnosis" class="screen-diagnosis" aria-labelledby="screen-diagnosis-title">
    <div class="screen-result-head">
      <h2 id="screen-diagnosis-title">{{ data?.code || code }} {{ data?.name }} · K线诊断</h2>
      <button type="button" class="secondary" autofocus @click="closeDlg">关闭</button>
    </div>
    <div class="screen-result-head">
      <label>诊断策略 <select v-model="strategy" @change="load"><option value="">全部启用策略</option>
        <option v-for="item in strategies" :key="item.id" :value="item.id">{{ item.name }}</option>
      </select></label>
      <span v-if="data" class="hint">行情时间 {{ data.asOf }}</span>
    </div>
    <p v-if="loading" class="hint" role="status">正在加载 K 线与诊断…</p>
    <p v-if="error" class="builder-field-error" role="alert">{{ error }} <button type="button" class="secondary" @click="load">重试</button></p>
    <p v-if="data?.warning || warning" class="hint" role="status">{{ data?.warning }} {{ warning }}</p>
    <template v-if="data">
      <div class="screen-chart-tools">
        <button type="button" class="secondary" :disabled="!chart" @click="chart?.fitContent()">查看全部K线</button>
      </div>
      <div ref="chart" class="chart-kline screen-kline"></div>
      <stock-info :code="data.code" :history-date="entry.slice(0,10)" />
      <details v-if="data.rules.length" class="ui-help" open><summary>逐项诊断</summary>
        <ul class="screen-rule-tree"><screen-rule-tree v-for="(rule, index) in data.rules" :key="index" :node="rule" /></ul>
        <ul v-if="data.explain?.length" class="screen-rule-tree">
          <li v-for="(step, index) in data.explain" :key="index">{{ step.matched ? '通过' : '未通过' }} · {{ step.name }} {{ step.detail }}</li>
        </ul>
      </details>
    </template>
  </dialog>
</teleport>
  `,
};
