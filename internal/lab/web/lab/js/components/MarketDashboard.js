import { Api } from '../api.js';
import { stockLabel } from '../format.js';
import { state } from '../store.js';

const exchangeNames = {sh: '沪市', sz: '深市', bj: '北交所'};
const breadthFields = [
  {key: 'up', label: '上涨'},
  {key: 'flat', label: '平盘'},
  {key: 'down', label: '下跌'},
  {key: 'unclassified', label: '缺少可比价格'},
];

const integer = new Intl.NumberFormat('zh-CN');
const decimal = new Intl.NumberFormat('zh-CN', {minimumFractionDigits: 2, maximumFractionDigits: 2});

export default {
  name: 'MarketDashboard',
  props: {active: Boolean},
  data() {
    return {snapshot: null, request: null, loading: false, error: '', ranking: 'amount', limit: 10, breadthFields};
  },
  computed: {
    stocks() {
      return this.snapshot?.stocks;
    },
    rankedStocks() {
      return this.stocks?.rankings[this.ranking].slice(0, this.limit) ?? [];
    },
  },
  watch: {
    active: {
      immediate: true,
      handler(active) {
        if (active) this.load();
        else this.cancelLoad();
      },
    },
  },
  beforeUnmount() {
    this.cancelLoad();
  },
  methods: {
    cancelLoad() {
      this.request?.abort();
      this.request = null;
      this.loading = false;
    },
    async load() {
      if (this.loading) return;
      const request = new AbortController();
      this.request = request;
      this.loading = true;
      this.error = '';
      try {
        const snapshot = await Api.marketDashboard(request.signal);
        if (this.request === request) this.snapshot = snapshot;
      } catch (err) {
        if (this.request === request) this.error = err.message || '日线行情读取失败，请重试';
      } finally {
        if (this.request === request) {
          this.request = null;
          this.loading = false;
        }
      }
    },
    exchangeName(code) { return exchangeNames[code]; },
    stockName(code) { return stockLabel(code, state.stockNames); },
    count(value) { return integer.format(value); },
    price(value) { return value == null ? '—' : decimal.format(value); },
    amount(value) { return value > 0 ? decimal.format(value / 1e8) : '—'; },
    change(value) {
      if (value == null) return '—';
      return (value > 0 ? '+' : '') + decimal.format(value) + '%';
    },
    direction(value) { return value > 0 ? 'pos' : value < 0 ? 'neg' : ''; },
  },
  template: `
<section class="market-dashboard" aria-label="A 股日线行情">
  <div class="market-toolbar">
    <div class="market-session">
      <strong>A 股日线</strong>
      <time v-if="snapshot?.date" :datetime="snapshot.date">{{ snapshot.date }}</time>
      <span class="market-source">MySQL 行情库</span>
    </div>
    <button type="button" class="secondary" :disabled="loading" @click="load">{{ error ? '重试读取' : '刷新行情' }}</button>
  </div>
  <div class="market-feedback">
    <p v-if="loading" class="hint" role="status">正在读取日线行情…</p>
    <p v-else-if="error" class="msg err" role="alert">
      {{ error }}<span v-if="snapshot">。下方仍显示上次读取的日线。</span>
    </p>
    <p v-else-if="snapshot && !snapshot.date" class="hint" role="status">行情库暂无上证指数交易日记录。</p>
  </div>

  <div class="market-content" :aria-busy="String(loading)">
    <section class="card market-index-panel" aria-labelledby="market-index-title">
      <h2 id="market-index-title">主要指数</h2>
      <div class="market-index-grid">
        <div class="market-index-quote" v-for="index in snapshot?.indexes ?? []" :key="index.code">
          <div class="market-index-name"><strong>{{ index.name }}</strong><span>{{ index.code }}</span></div>
          <div class="market-index-numbers">
            <strong>{{ price(index.close) }}</strong>
            <span :class="direction(index.change)">{{ change(index.change) }}</span>
          </div>
          <p v-if="!index.date || index.date !== snapshot.date" class="hint market-index-date">
            <time v-if="index.date" :datetime="index.date">{{ index.date }}</time>
            <span v-else>暂无日线</span>
          </p>
        </div>
        <div v-if="!snapshot" class="empty market-index-empty">{{ loading ? '正在读取指数收盘数据' : '暂无指数收盘数据' }}</div>
      </div>
    </section>

    <div class="market-panels">
      <section class="card market-breadth-panel" aria-labelledby="market-breadth-title">
        <h2 id="market-breadth-title">沪深北成交概况</h2>
        <template v-if="stocks?.total">
          <dl class="market-totals">
            <div><dt>成交额</dt><dd>{{ amount(stocks.amount) }} <small v-if="stocks.withAmount">亿元</small></dd></div>
            <div><dt>有成交股票</dt><dd>{{ count(stocks.total) }} <small>只</small></dd></div>
          </dl>
          <div class="market-breadth-bar" aria-hidden="true">
            <span v-for="item in breadthFields" :key="item.key" :data-side="item.key" :style="{flexGrow: stocks[item.key]}"></span>
          </div>
          <dl class="market-breadth-legend">
            <div v-for="item in breadthFields" :key="item.key">
              <dt :class="direction(item.key === 'up' ? 1 : item.key === 'down' ? -1 : 0)">{{ item.label }}</dt>
              <dd>{{ count(stocks[item.key]) }}</dd>
            </div>
          </dl>
          <div class="detail-wrap market-table-wrap" role="region" aria-label="分市场成交概况" tabindex="0">
            <table aria-label="沪深北各市场成交额和涨跌家数">
              <thead><tr><th scope="col">市场</th><th scope="col">成交额（亿元）</th><th scope="col">上涨</th><th scope="col">下跌</th></tr></thead>
              <tbody>
                <tr v-for="row in stocks.exchanges" :key="row.exchange">
                  <th scope="row">{{ exchangeName(row.exchange) }}</th>
                  <td>{{ amount(row.amount) }}</td>
                  <td class="pos">{{ row.total ? count(row.up) : '—' }}</td>
                  <td class="neg">{{ row.total ? count(row.down) : '—' }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <p class="hint" v-if="stocks.withAmount < stocks.total">成交额缺失 {{ count(stocks.total - stocks.withAmount) }} 只，合计及榜单按有效金额统计。</p>
        </template>
        <p v-else class="empty">{{ loading ? '正在读取成交概况' : '暂无当日有效成交记录' }}</p>
      </section>

      <section class="card market-rank-panel" aria-labelledby="market-rank-title">
        <div class="market-rank-toolbar">
          <h2 id="market-rank-title">个股排行</h2>
          <div class="filters market-rank-filters">
            <label>
              <span class="market-sr-only">榜单类型</span>
              <select v-model="ranking">
                <option value="amount">成交额榜</option>
                <option value="up">涨幅榜</option>
                <option value="down">跌幅榜</option>
              </select>
            </label>
            <label>
              <span class="market-sr-only">显示名次</span>
              <select v-model.number="limit">
                <option :value="10">前 10 名</option>
                <option :value="20">前 20 名</option>
                <option :value="50">前 50 名</option>
              </select>
            </label>
          </div>
        </div>
        <div v-if="rankedStocks.length" class="detail-wrap market-table-wrap market-ranking" role="region" aria-label="个股排行表格" tabindex="0">
          <table aria-label="当前个股排行">
            <thead>
              <tr>
                <th scope="col">排名</th><th scope="col">股票</th><th scope="col">收盘（元）</th>
                <th scope="col" :aria-sort="ranking === 'up' ? 'descending' : ranking === 'down' ? 'ascending' : undefined">涨跌幅</th>
                <th scope="col" :aria-sort="ranking === 'amount' ? 'descending' : undefined">成交额（亿元）</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(row, i) in rankedStocks" :key="row.code">
                <td>{{ i + 1 }}</td>
                <th scope="row">{{ stockName(row.code) }}</th>
                <td>{{ price(row.close) }}</td>
                <td :class="direction(row.change)">{{ change(row.change) }}</td>
                <td>{{ amount(row.amount) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <p v-else class="empty">{{ loading ? '正在读取个股排行' : '当前榜单暂无数据' }}</p>
        <p v-if="stocks?.total" class="hint">
          {{ count(rankedStocks.length) }} 只 · 个股涨跌幅按未复权收盘价计算<span v-if="snapshot.previousDate">，对比 {{ snapshot.previousDate }}</span>。
        </p>
      </section>
    </div>
  </div>
</section>
  `,
};
