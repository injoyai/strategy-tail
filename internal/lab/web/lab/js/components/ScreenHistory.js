import { state } from '../store.js';
import { Api } from '../api.js';
import { mountTradeTable } from '../trade-table.js';

export default {
  name: 'ScreenHistory',
  props: {active: Boolean, mode: {type: String, required: true}, strategies: {type: Array, required: true}, revision: Number, saleRevision: Number},
  emits: ['diagnose'],
  inject: ['startTask'],
  data() {
    return {data: null, loading: false, error: '', busy: false, seq: 0, timer: null,
      page: 1, strategy: '', days: '', status: '', code: '', search: '', tag: '', tagSearch: '',
      table: null, tableError: ''};
  },
  computed: {
    taskRunning() { return state.taskRunning; },
    query() { return {mode: this.mode, page: this.page, strategy: this.strategy, days: this.days,
      status: this.status, code: this.code, tag: this.tag}; },
    stats() { return this.data?.stats; },
    sellMode() { return this.mode === 'sell'; },
  },
  watch: {
    active(value) {
      this.seq++;
      this.loading = false;
      clearTimeout(this.timer);
      if (value) this.load();
      else this.dropTable();
    },
    revision() { if (this.active) this.load(); },
    saleRevision() { if (this.active) this.load(); },
    mode() {
      this.data = null;
      this.error = '';
      this.status = '';
      this.days = '';
      this.tag = '';
      this.tagSearch = '';
      this.page = 1;
    },
    query() { if (this.active) this.load(); },
  },
  mounted() { if (this.active) this.load(); },
  beforeUnmount() {
    this.seq++;
    clearTimeout(this.timer);
    this.dropTable();
  },
  methods: {
    dropTable() {
      this.table?.destroy();
      this.table = null;
    },
    syncTable() {
      if (!this.$refs.table || !this.active) return;
      try {
        if (!this.table) this.table = mountTradeTable(this.$refs.table, row => this.$emit('diagnose', row),
          error => { this.tableError = error.message; }, {screen: true});
        const names = new Map(this.strategies.map(item => [item.id, item.name]));
        this.table.setData((this.data?.rows || []).map(row => ({
          ...row, _rowId: [row.strategy, row.code, row.buyTime].join('/'),
          variant: row.strategyName || names.get(row.strategy) || row.strategy,
          stockLabel: [row.code, row.name].filter(Boolean).join(' '),
          tagText: row.tags?.join(' · ') || '',
          lastPrice: row.lastPrice || null, sellPrice: row.sellTime ? row.sellPrice : null,
        })));
        this.tableError = '';
      } catch (error) {
        this.tableError = error.message;
      }
    },
    async load(reportFailure = false) {
      clearTimeout(this.timer);
      const seq = ++this.seq;
      this.loading = true;
      try {
        const next = await Api.screenHistory(this.query);
        if (seq !== this.seq) return;
        this.error = '';
        this.data = next;
        if (this.page !== next.page) this.page = next.page;
        await this.$nextTick();
        if (seq === this.seq) this.syncTable();
      } catch (error) {
        if (seq === this.seq) {
          this.error = error.message;
          if (reportFailure === true) throw error;
        }
      } finally {
        if (seq === this.seq) {
          this.loading = false;
          if (this.active) this.timer = setTimeout(this.load, 10000);
        }
      }
    },
    async rebuild() {
      if (this.busy || this.taskRunning) return;
      this.busy = true;
      try {
        await Api.refreshScreenHistory();
        this.startTask('screen_history', {phase: 'preparing'});
        await this.load();
      } catch (error) {
        this.error = error.message;
      } finally {
        this.busy = false;
      }
    },
    applySearch() {
      this.page = 1;
      this.code = this.search.trim();
      this.tag = this.tagSearch.trim();
    },
    clearSearch(field) {
      this[field] = '';
      this.applySearch();
      this.$refs[field]?.focus();
    },
  },
  template: `
<section class="card screen-results" :class="{'has-rows': data?.total}" :aria-busy="loading || !!data?.running">
  <form class="screen-history-filters" novalidate @submit.prevent="applySearch">
    <label>策略 <select v-model="strategy" @change="page = 1"><option value="">全部</option>
      <option v-for="item in strategies" :key="item.id" :value="item.id">{{ item.name }}</option>
    </select></label>
    <label v-if="!sellMode">买入日期 <select v-model="days" @change="page = 1"><option value="">全部</option>
      <option v-for="days in [15,30,60,120,240]" :key="days" :value="days">近 {{ days }} 天</option>
    </select></label>
    <label v-if="!sellMode">状态 <select v-model="status" @change="page = 1"><option value="">全部</option>
      <option value="sold">已卖出</option><option value="open">持有</option>
    </select></label>
    <label>股票 <input ref="search" v-model="search" type="text" placeholder="代码或名称"></label>
    <button v-if="search" type="button" class="secondary" aria-label="清除股票筛选" @click="clearSearch('search')">清除</button>
    <label v-if="!sellMode">标签 <input ref="tagSearch" v-model="tagSearch" type="text" placeholder="完整标签"></label>
    <button v-if="!sellMode && tagSearch" type="button" class="secondary" aria-label="清除标签筛选" @click="clearSearch('tagSearch')">清除</button>
    <button type="submit" class="secondary">筛选</button>
    <button v-if="!sellMode" type="button" class="secondary screen-rebuild" :disabled="busy || taskRunning" @click="rebuild">重算历史</button>
  </form>
  <div v-if="stats?.total || data?.through" class="screen-summary">
    <p v-if="stats?.total" class="hint" role="status">
      已卖出 {{ stats.total }} 笔 · 胜率 {{ stats.winRate.toFixed(1) }}% · 盈亏比 {{ stats.profitFactor == null ? '∞' : stats.profitFactor.toFixed(2) }} · 平均持有 {{ data.avgHoldingDays.toFixed(1) }} 天 · 未扣交易成本
    </p>
    <p v-if="data?.through" class="hint">上次重算截至 {{ data.through }} · 回看 {{ data.lookback }} 个交易日</p>
  </div>
  <p v-if="data?.archiveError" class="builder-field-error" role="alert">{{ data.archiveError }}</p>
  <p v-if="error" class="builder-field-error" role="alert">{{ error }} <button type="button" class="secondary" @click="load">重试读取</button></p>
  <p v-if="tableError" class="builder-field-error" role="alert">{{ tableError }} <button type="button" class="secondary" @click="syncTable">重试表格</button></p>
  <details v-if="data?.failedCodes?.length" class="ui-help"><summary>{{ data.failedCodes.length }} 只股票行情读取失败</summary><p>{{ data.failedCodes.join('、') }}</p></details>
  <div v-show="data?.total > 0" ref="table" class="trade-table screen-signal-table" aria-label="策略信号表格"></div>
  <p v-if="!data?.total && !data?.running && !error" class="screen-empty" role="status">{{ loading ? '正在读取记录…' : sellMode ? '今日暂无已入库的卖点。' : '当前条件下暂无交易记录。' }}</p>
  <div v-if="data?.total" class="screen-pagination">
    <span>共 {{ data.total }} 条 · 第 {{ data.page }} / {{ data.pages }} 页</span>
    <button type="button" class="secondary" :disabled="page <= 1 || loading" @click="page--">上一页</button>
    <button type="button" class="secondary" :disabled="page >= data.pages || loading" @click="page++">下一页</button>
  </div>
</section>
  `,
};
