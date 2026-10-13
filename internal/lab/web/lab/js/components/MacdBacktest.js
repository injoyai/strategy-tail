import { state } from '../store.js';
import { Api } from '../api.js';

export const MACD_GROUPS = [
  {title: '买入过滤', conditions: [{key:'floatCap',label:'流通市值过滤'},{key:'price',label:'价格过滤'},{key:'limitUp',label:'过滤涨停'}], fields: [
    {key: 'minFloatCap', condition: 'floatCap', label: '流通市值下限', unit: '亿元', min: 0, step: 10},
    {key: 'maxPrice', condition: 'price', label: '价格上限', unit: '元', min: 0, step: 1},
  ]},
  {title: 'MACD 买入条件', conditions: [{key:'negative',label:'MACD 负柱天数'},{key:'rising',label:'MACD 连涨天数'}], fields: [
    {key: 'negativeMinDays', condition: 'negative', label: '负柱天数下限', unit: '天', min: 1, step: 1},
    {key: 'negativeMaxDays', condition: 'negative', label: '负柱天数上限', unit: '天', min: 0, step: 1},
    {key: 'risingMinDays', condition: 'rising', label: '连涨天数下限', unit: '天', min: 1, step: 1},
    {key: 'risingMaxDays', condition: 'rising', label: '连涨天数上限', unit: '天', min: 0, step: 1},
  ], hint: '天数上限为 0 或小于下限时，按至少 N 天判断。'},
  {title: '均线买入条件', conditions: [{key:'priceMA',label:'收盘价高于均线'},{key:'trend1',label:'趋势均线一向上'},{key:'trend2',label:'趋势均线二向上'}], fields: [
    {key: 'priceMAPeriod', condition: 'priceMA', label: '收盘价高于均线', unit: '日', min: 1, step: 1},
    {key: 'trendPeriod1', condition: 'trend1', label: '趋势均线一周期', unit: '日', min: 1, step: 1},
    {key: 'trendMinSlope1', condition: 'trend1', label: '均线一最小日斜率', unit: '%', min: 0, step: 0.01},
    {key: 'trendPeriod2', condition: 'trend2', label: '趋势均线二周期', unit: '日', min: 1, step: 1},
    {key: 'trendMinSlope2', condition: 'trend2', label: '均线二最小日斜率', unit: '%', min: 0, step: 0.01},
  ], hint: '勾选的趋势均线须连续 5 步向上，并满足各自的最小日斜率。'},
  {title: '卖出条件', conditions: [{key:'sellMACD',label:'MACD 反转卖出'}], fields: [
    {key: 'sellLookback', condition: 'sellMACD', label: '高点回看天数', unit: '天', min: 1, step: 1},
  ]},
];
export function macdEnabled(p, key) { return p.enabled == null || p.enabled[key] === true; }
const slopeKeys = ['trendMinSlope1', 'trendMinSlope2'];

export default {
  name: 'MacdBacktest',
  inject: ['startTask'],
  setup() { return {state, groups: MACD_GROUPS}; },
  data() { return {loading: false, submitting: false, error: '', errors: {}, defaults: null}; },
  computed: { cfg() { return state.macdCfg; } },
  mounted() { this.load(); },
  methods: {
    enabled(key) { return macdEnabled(this.cfg.macd, key); },
    clearConditionErrors(key) { for (const g of MACD_GROUPS) for (const f of g.fields) if (f.condition === key) delete this.errors[f.key]; },
    async load() {
      this.loading = true;
      this.error = '';
      try {
        this.defaults = await Api.macdConfig();
        if (!state.macdCfg) this.reset();
      } catch (e) { this.error = '加载回测参数失败：' + e.message; }
      finally { this.loading = false; }
    },
    reset() {
      const c = JSON.parse(JSON.stringify(this.defaults));
      slopeKeys.forEach(k => { c.macd[k] *= 100; });
      c.sampleCodesText = '';
      c.sampleSize = 50;
      state.macdCfg = c;
      this.errors = {};
      this.error = '';
    },
    validate() {
      const errors = {};
      for (const group of MACD_GROUPS) for (const f of group.fields) {
        if (!this.enabled(f.condition)) continue;
        const v = this.cfg.macd[f.key];
        if (v === '' || !Number.isFinite(v) || v < f.min ||
          (f.unit === '天' || f.unit === '日') && (!Number.isInteger(v) || v > 1000)) {
          errors[f.key] = f.unit === '天' || f.unit === '日' ? `请输入 ${f.min}～1000 的整数` : '请输入非负数值';
        }
      }
      for (const key of ['startYear', 'endYear']) {
        const v = this.cfg[key];
        if (!Number.isInteger(v) || v < 1990 || v > this.defaults.endYear) errors[key] = `请输入 1990～${this.defaults.endYear} 的年份`;
      }
      if (this.cfg.startYear > this.cfg.endYear) errors.endYear = '结束年份不能早于起始年份';
      if (this.cfg.sampleMode === 'random' && (!Number.isInteger(this.cfg.sampleSize) || this.cfg.sampleSize < 1)) errors.sampleSize = '请输入正整数';
      const codes = this.cfg.sampleCodesText.split(/[,，\s]+/).filter(Boolean);
      if (this.cfg.sampleMode === 'codes' && (!codes.length || codes.some(c => !/^(sh|sz)\d{6}$/.test(c)))) errors.sampleCodes = '请输入带 sh 或 sz 前缀的六位股票代码';
      this.errors = errors;
      if (Object.keys(errors).length) {
        this.$nextTick(() => this.$el.querySelector('[aria-invalid="true"]')?.focus());
        return null;
      }
      const c = JSON.parse(JSON.stringify(this.cfg));
      delete c.sampleCodesText;
      c.sampleCodes = [...new Set(codes)];
      for (const group of MACD_GROUPS) for (const f of group.fields) {
        if (!this.enabled(f.condition) && !Number.isFinite(c.macd[f.key])) delete c.macd[f.key];
      }
      slopeKeys.forEach(k => { c.macd[k] /= 100; });
      return c;
    },
    async run() {
      if (this.submitting || state.taskRunning || state.dataUpdating) return;
      const cfg = this.validate();
      if (!cfg) return;
      this.submitting = true;
      this.error = '';
      try {
        await Api.runMACD(cfg);
        state.scriptMsg = {text: '参数回测已开始，完成后自动显示结果。', ok: true};
        this.startTask('backtest');
      } catch (e) { this.error = '启动回测失败：' + e.message; }
      finally { this.submitting = false; }
    },
  },
  template: `
<form class="macd-form" novalidate @submit.prevent="run">
  <div class="macd-toolbar">
    <span class="hint">勾选条件自由组合 · 买入条件同时满足</span>
    <div class="editor-actions">
      <button type="button" class="secondary" @click="reset" :disabled="!cfg || submitting || state.taskRunning">恢复默认参数</button>
      <button class="primary" type="submit" :disabled="!cfg || loading || submitting || state.taskRunning || state.dataUpdating" :aria-busy="String(submitting)">运行回测</button>
    </div>
  </div>
  <div v-if="loading" role="status" class="hint">正在加载参数…</div>
  <div v-if="error" role="alert" class="msg err">{{ error }} <button v-if="!cfg" type="button" class="secondary" @click="load">重新加载</button></div>
  <div v-if="cfg" class="grid2 macd-config-grid">
    <div class="card macd-range">
      <h3>回测范围</h3>
      <div class="macd-years">
      <div class="macd-field" v-for="f in [{key:'startYear',label:'起始年份'},{key:'endYear',label:'结束年份'}]" :key="f.key">
        <label :for="'macd-' + f.key">{{ f.label }}</label>
        <div class="macd-input"><input :id="'macd-' + f.key" type="number" min="1990" :max="defaults.endYear" v-model.number="cfg[f.key]" :aria-invalid="String(!!errors[f.key])" :aria-describedby="errors[f.key] ? 'macd-error-' + f.key : null" @input="delete errors[f.key]"></div>
        <span v-if="errors[f.key]" :id="'macd-error-' + f.key" class="field-error">{{ errors[f.key] }}</span>
      </div>
      </div>
      <div class="form-row"><label for="macd-sampleMode">股票范围</label><select id="macd-sampleMode" v-model="cfg.sampleMode"><option value="all">全部沪深主板</option><option value="random">随机 N 只</option><option value="codes">指定股票</option></select></div>
      <div v-if="cfg.sampleMode==='random'" class="macd-field"><label for="macd-sampleSize">随机数量</label><div class="macd-input"><input id="macd-sampleSize" type="number" min="1" v-model.number="cfg.sampleSize" :aria-invalid="String(!!errors.sampleSize)" aria-describedby="macd-error-sampleSize"></div><span id="macd-error-sampleSize" class="field-error">{{ errors.sampleSize || '' }}</span></div>
      <div v-if="cfg.sampleMode==='codes'" class="macd-field"><label for="macd-sampleCodes">股票代码</label><div class="macd-input"><input id="macd-sampleCodes" type="text" v-model="cfg.sampleCodesText" placeholder="sh600000,sz000001" :aria-invalid="String(!!errors.sampleCodes)" aria-describedby="macd-error-sampleCodes"></div><span id="macd-error-sampleCodes" class="field-error">{{ errors.sampleCodes || '' }}</span></div>
      <p class="hint">成本和单票仓位沿用现有配置文件。分钟线缺失时沿用引擎的日线退化行为。</p>
      <p class="hint">未勾选的条件不参与回测；全部买入条件取消时，按全部样本买入；取消卖出条件时，仅在期末虚拟平仓。</p>
      <p class="hint">勾选状态和参数随结果保存；取消勾选会保留参数值。</p>
      <div class="msg" role="status" aria-live="polite">{{ state.scriptMsg.text }}</div>
    </div>
    <div class="macd-groups">
      <fieldset v-for="group in groups" :key="group.title" class="card macd-group">
        <legend>{{ group.title }}</legend>
        <div class="macd-conditions">
          <label v-for="c in group.conditions" :key="c.key" class="checkbox-label"><input type="checkbox" v-model="cfg.macd.enabled[c.key]" @change="clearConditionErrors(c.key)">{{ c.label }}</label>
        </div>
        <div class="macd-fields">
          <div v-for="f in group.fields" :key="f.key" class="macd-field" :class="{'condition-disabled': !enabled(f.condition)}">
            <label :for="'macd-' + f.key">{{ f.label }}</label>
            <div class="macd-input"><input :id="'macd-' + f.key" type="number" v-model.number="cfg.macd[f.key]" :disabled="!enabled(f.condition)" :min="f.min" :step="f.step" :aria-invalid="String(!!errors[f.key])" :aria-describedby="errors[f.key] ? 'macd-error-' + f.key : null" @input="delete errors[f.key]"><span>{{ f.unit }}</span></div>
            <span v-if="errors[f.key]" :id="'macd-error-' + f.key" class="field-error">{{ errors[f.key] }}</span>
          </div>
        </div>
        <p v-if="group.hint" class="hint">{{ group.hint }}</p>
        <p v-if="group.title==='买入过滤'" class="hint">市值或价格填 0 时不限制该项。</p>
      </fieldset>
    </div>
  </div>
</form>
  `,
};
