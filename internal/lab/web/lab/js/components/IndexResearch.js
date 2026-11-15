import { markRaw } from '../vendor/vue.esm-browser.prod.js';
import { Api } from '../api.js';
import { dropChart, mountChart, NAV_COLORS } from '../echarts.js';

const dateFormat = new Intl.DateTimeFormat('sv-SE', {timeZone: 'Asia/Shanghai', dateStyle: 'short'});
const numberFormat = new Intl.NumberFormat('zh-CN', {maximumFractionDigits: 2, minimumFractionDigits: 2});
const countFormat = new Intl.NumberFormat('zh-CN');
const pct = value => `${numberFormat.format(value * 100)}%`;
const close = value => `${numberFormat.format(value)} 点`;
const indexLabel = item => item.name ? `${item.name} ${item.code}` : item.code;
const stockCount = value => value.shanghai + value.shenzhen + value.beijing;

function coverage(value) {
  return `沪 ${countFormat.format(value.shanghai)} · 深 ${countFormat.format(value.shenzhen)} · 北 ${countFormat.format(value.beijing)}`;
}

function chartOption(points, indexName) {
  const dates = points.map(point => point.date);
  const axis = {type: 'category', boundaryGap: false, data: dates};
  return {
    animation: false,
    backgroundColor: 'transparent',
    title: [
      {text: `${indexName} · 收盘点位`, left: 8, top: 3, textStyle: {fontSize: 13, color: NAV_COLORS.gross}},
      {text: '成交拥挤度 · %', left: 8, top: '46%', textStyle: {fontSize: 13, color: NAV_COLORS.net}},
    ],
    grid: [{left: 62, right: 18, top: 39, height: '32%'}, {left: 62, right: 18, top: '54%', bottom: 66}],
    xAxis: [{...axis, axisLabel: {show: false}, axisTick: {show: false}}, {...axis, gridIndex: 1, axisLabel: {hideOverlap: true}}],
    yAxis: [{type: 'value', scale: true}, {type: 'value', gridIndex: 1, scale: true, axisLabel: {formatter: '{value}%'}}],
    axisPointer: {link: [{xAxisIndex: 'all'}]},
    tooltip: {
      trigger: 'axis', axisPointer: {type: 'line'}, renderMode: 'richText', confine: true,
      formatter: params => {
        const point = points[params[0]?.dataIndex];
        if (!point) return '';
        const sample = point.concentration;
        const lines = [point.date, `指数收盘 ${point.close == null ? '缺日线' : close(point.close)}`,
          `成交拥挤度 ${sample?.share == null ? '无有效值' : pct(sample.share)}`];
        if (sample) {
          lines.push(`入选 ${countFormat.format(sample.selected)} / ${countFormat.format(stockCount(sample))} 只`, coverage(sample));
          if (sample.excluded) lines.push(`成交额缺失或无效 ${countFormat.format(sample.excluded)} 只`);
        }
        return lines.join('\n');
      },
    },
    dataZoom: [
      {type: 'inside', xAxisIndex: [0, 1], zoomOnMouseWheel: 'ctrl', moveOnMouseWheel: false},
      {type: 'slider', xAxisIndex: [0, 1], bottom: 9, height: 22},
    ],
    series: [
      {type: 'line', color: NAV_COLORS.gross, data: points.map(p => p.close), lineStyle: {width: 2}},
      {type: 'line', color: NAV_COLORS.net, xAxisIndex: 1, yAxisIndex: 1,
        data: points.map(p => p.concentration?.share == null ? null : p.concentration.share * 100), lineStyle: {width: 2}},
    ].map(series => ({...series, symbolSize: 4,
      showSymbol: series.data.some((value, i, data) => value != null && data[i - 1] == null && data[i + 1] == null)})),
  };
}

export default {
  name: 'IndexResearch',
  props: {active: Boolean},
  data() {
    return {
      indexes: null, catalogError: '', catalogBusy: false, busy: false, error: '', invalidField: '',
      query: {index: '', from: '2000-01-01', to: dateFormat.format(new Date()), topPercent: 5},
      result: null, chart: null, chartError: false, request: null, inspected: null,
    };
  },
  computed: {
    latestShare() { return this.result?.points.findLast(p => p.concentration?.share != null); },
    latestClose() { return this.result?.points.findLast(p => p.close != null); },
    latestSample() { return this.result?.points.findLast(p => p.concentration); },
    excludedDays() { return this.result?.points.filter(p => p.concentration?.excluded > 0).length || 0; },
  },
  watch: {
    active: {immediate: true, handler(active) {
      if (!active) return;
      if (!this.indexes && !this.catalogError) this.loadIndexes();
      else if (!this.chart) this.$nextTick(this.renderChart);
    }},
  },
  beforeUnmount() {
    this.request?.abort();
    dropChart(this.chart);
  },
  methods: {
    indexLabel, pct, close, coverage, stockCount,
    async loadIndexes() {
      if (this.catalogBusy) return;
      this.catalogBusy = true;
      this.catalogError = '';
      const request = this.request = markRaw(new AbortController());
      try {
        this.indexes = await Api.researchIndexes(request.signal);
      } catch (err) {
        if (request.signal.aborted) return;
        this.catalogError = err.message;
      } finally {
        this.catalogBusy = false;
      }
      if (!this.indexes?.length) return;
      this.query.index = (this.indexes.find(item => item.code === 'sh000001') || this.indexes[0]).code;
      this.calculate();
    },
    async calculate() {
      if (this.busy || !this.query.index) return;
      const rule = {...this.query};
      this.invalidField = !rule.from ? 'from' : !rule.to || rule.from > rule.to ? 'to' :
        !Number.isInteger(rule.topPercent) || rule.topPercent < 1 || rule.topPercent > 100 ? 'topPercent' : '';
      if (this.invalidField) {
        this.error = this.invalidField === 'topPercent' ? '前 N% 请输入 1 至 100 的整数' : '请输入有效的起止日期，结束日期须在起始日期当天或之后';
        this.$refs[this.invalidField]?.focus();
        return;
      }
      this.busy = true;
      this.error = '';
      const item = this.indexes.find(index => index.code === rule.index);
      const request = this.request = markRaw(new AbortController());
      try {
        const points = await Api.indexResearch(rule, request.signal);
        this.result = {rule, indexName: item?.name || rule.index, points};
        this.inspected = null;
        dropChart(this.chart);
        this.chart = null;
        this.chartError = false;
      } catch (err) {
        if (request.signal.aborted) return;
        this.error = err.message;
      } finally {
        this.busy = false;
      }
      this.$nextTick(this.renderChart);
    },
    renderChart() {
      if (!this.active || !this.$refs.chart || this.chart) return;
      try {
        this.chart = mountChart(this.$refs.chart);
        if (this.chart) this.chart.setOption(chartOption(this.result.points, this.result.indexName), true);
        this.chartError = !this.chart;
      } catch {
        dropChart(this.chart);
        this.chart = null;
        this.chartError = true;
      }
    },
    onChartKey(event) {
      if (!this.chart || event.altKey || event.ctrlKey || event.metaKey) return;
      const points = this.result.points;
      if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
        const current = this.inspected ?? points.length - 1;
        this.inspected = Math.max(0, Math.min(points.length - 1, current + (event.key === 'ArrowLeft' ? -1 : 1)));
        const point = points[this.inspected];
        this.chart.dispatchAction({type: 'showTip', seriesIndex: point.close == null ? 1 : 0, dataIndex: this.inspected});
      } else if (['+', '=', '-', 'Home'].includes(event.key)) {
        const {start, end} = this.chart.getOption().dataZoom[0];
        const center = (start + end) / 2;
        const halfWidth = Math.max(100 / points.length, (end - start) * (event.key === '-' ? 0.7 : 0.35));
        this.chart.dispatchAction({type: 'dataZoom', dataZoomIndex: 0,
          start: event.key === 'Home' ? 0 : Math.max(0, center - halfWidth),
          end: event.key === 'Home' ? 100 : Math.min(100, center + halfWidth)});
      } else return;
      event.preventDefault();
    },
  },
  template: `
<div class="index-research">
  <p v-if="catalogBusy" class="hint" role="status">正在读取指数目录…</p>
  <div v-else-if="catalogError" class="card index-research-feedback">
    <p class="msg err" role="alert">{{ catalogError }}</p>
    <button type="button" class="secondary" @click="loadIndexes">重试读取</button>
  </div>
  <p v-else-if="indexes && !indexes.length" class="hint" role="status">行情库暂无指数日线。</p>
  <template v-if="indexes?.length">
    <form class="card index-research-controls" novalidate @submit.prevent="calculate">
      <fieldset :disabled="busy" aria-label="成交拥挤度规则">
        <div class="form-row index-research-index"><label for="indexResearchIndex">对照指数</label>
          <select id="indexResearchIndex" v-model="query.index">
            <option v-for="item in indexes" :key="item.code" :value="item.code">{{ indexLabel(item) }}</option>
          </select>
        </div>
        <div class="form-row"><label for="indexResearchFrom">起始日期</label>
          <input id="indexResearchFrom" ref="from" type="date" v-model="query.from" required :aria-invalid="invalidField === 'from'" aria-describedby="indexResearchError">
        </div>
        <div class="form-row"><label for="indexResearchTo">结束日期</label>
          <input id="indexResearchTo" ref="to" type="date" v-model="query.to" required :aria-invalid="invalidField === 'to'" aria-describedby="indexResearchError">
        </div>
        <div class="form-row index-research-top"><label for="indexResearchTop">成交额前 N%</label>
          <input id="indexResearchTop" ref="topPercent" type="number" v-model.number="query.topPercent" min="1" max="100" step="1" required :aria-invalid="invalidField === 'topPercent'" aria-describedby="indexResearchError">
        </div>
        <button type="submit" class="primary" :aria-busy="busy">绘制曲线</button>
      </fieldset>
      <p v-if="busy" class="hint" role="status">正在读取日线并计算成交拥挤度…</p>
      <p id="indexResearchError" class="msg err" role="alert">{{ error }}</p>
    </form>
    <div v-if="result" class="card index-research-result">
      <div class="index-research-readout" role="status">
        <p v-if="latestClose"><span>{{ result.indexName }}</span> <strong>{{ close(latestClose.close) }}</strong> <time>{{ latestClose.date }}</time></p>
        <p v-else>所选区间无指数收盘数据</p>
        <p v-if="latestShare"><span>成交拥挤度</span> <strong>{{ pct(latestShare.concentration.share) }}</strong> <time>{{ latestShare.date }}</time></p>
        <p v-else>所选区间无有效成交拥挤度数据</p>
      </div>
      <p class="hint index-research-rule">{{ result.rule.from }} 至 {{ result.rule.to }} · 成交额前 {{ result.rule.topPercent }}% 的股票占当日样本成交额的比例</p>
      <div v-if="latestClose || latestShare" ref="chart" class="index-research-chart" v-show="!chartError" tabindex="0" role="group"
        aria-label="指数收盘点位和成交拥挤度曲线，左右键查值，加减键缩放，Home 显示全程" @keydown="onChartKey"></div>
      <p v-if="chartError" class="hint" role="status">图表组件加载失败。可查看上方最近一个有效交易日的数据。</p>
      <p v-if="inspected !== null" class="hint" role="status">
        {{ result.points[inspected].date }} · 收盘 {{ result.points[inspected].close == null ? '缺日线' : close(result.points[inspected].close) }}
        · 拥挤度 {{ result.points[inspected].concentration?.share == null ? '无有效值' : pct(result.points[inspected].concentration.share) }}
      </p>
      <p class="hint index-research-coverage">
        样本为库内当日有有效成交的沪深北 A 股，含 ST，覆盖完整性未核验。
        <template v-if="latestSample">{{ latestSample.date }} 样本 {{ stockCount(latestSample.concentration).toLocaleString('zh-CN') }} 只（{{ coverage(latestSample.concentration) }}）。</template>
        <template v-if="excludedDays">{{ excludedDays }} 个交易日排除了成交额缺失或无效的股票。</template>
      </p>
    </div>
  </template>
</div>
`,
};
