import { state } from '../store.js';
import { ACCOUNT_CONFIG } from '../config.js';
import { dlgMixin } from './dlgMixin.js';

export default {
  name: 'RunConfigDlg',
  mixins: [dlgMixin],
  data() {
    return {cfg: {}, mode: 'builder', run: null, submitting: false, error: '', errorKey: ''};
  },
  setup() {
    return {
      account: ACCOUNT_CONFIG,
      years: [{key: 'startYear', label: '开始年份'}, {key: 'endYear', label: '结束年份'}],
      exits: [
        {on: 'holdingOn', key: 'holdingDays', label: '持仓上限', unit: '交易日', step: 1},
        {on: 'tpOn', key: 'takeProfit', label: '止盈', unit: '%', step: 'any'},
        {on: 'slOn', key: 'stopLoss', label: '止损', unit: '%', step: 'any'},
      ],
    };
  },
  methods: {
    openDlg(mode, run) {
      if (this.submitting || state.taskRunning) return;
      this.cfg = {...state.runCfg};
      this.mode = mode;
      this.run = run;
      this.error = ''; this.errorKey = '';
      this.open = true;
      this.$nextTick(() => this.$el.querySelector('#run-startYear')?.focus());
    },
    closeDlg() {
      if (this.submitting) return;
      this.open = false;
      this.run = null;
    },
    cancel(event) {
      event.preventDefault();
      this.closeDlg();
    },
    invalid(key, text) {
      this.errorKey = key; this.error = text;
      this.$nextTick(() => this.$el.querySelector('[aria-invalid="true"]')?.focus());
      return false;
    },
    validate() {
      const c = this.cfg;
      const minYear = this.mode === 'builder' ? 1990 : 1;
      if (!Number.isInteger(c.startYear) || c.startYear < minYear) return this.invalid('startYear', `开始年份需为不小于 ${minYear} 的整数`);
      if (!Number.isInteger(c.endYear) || c.endYear < c.startYear || c.endYear > new Date().getFullYear()) return this.invalid('endYear', '结束年份需为整数，不早于开始年份且不超过当前年份');
      if (!['all', 'random', 'codes'].includes(c.sampleMode)) return this.invalid('sampleMode', '请选择股票范围');
      if (c.sampleMode === 'random' && (!Number.isInteger(c.sampleSize) || c.sampleSize <= 0)) return this.invalid('sampleSize', '随机样本数需为正整数');
      if (c.sampleMode === 'codes' && !c.sampleCodes.split(',').some(s => s.trim())) return this.invalid('sampleCodes', '请填写股票代码，用英文逗号分隔');
      if (this.mode !== 'builder') {
        for (const e of this.exits) {
          if (c[e.on] && (!Number.isFinite(c[e.key]) || c[e.key] <= 0 || (e.step === 1 && !Number.isInteger(c[e.key])))) return this.invalid(e.key, `${e.label}需为${e.step === 1 ? '正整数' : '正数'}`);
        }
        if (!this.exits.some(e => c[e.on])) return this.invalid('holdingOn', '卖出规则至少启用一项');
      }
      return true;
    },
    async confirm() {
      if (this.submitting || !this.run) return;
      this.error = ''; this.errorKey = '';
      if (state.taskRunning) { this.error = '已有任务运行中，请稍后再试'; return; }
      if (!this.validate()) return;
      this.submitting = true;
      Object.assign(state.runCfg, this.cfg);
      let started = false;
      try {
        started = await this.run();
        if (!started) this.error = (this.mode === 'builder' ? state.builderMsg.text : state.scriptMsg.text) || '回测未启动，请检查策略和配置';
      } catch (e) { this.error = e.message; }
      finally { this.submitting = false; }
      if (started) this.closeDlg();
    },
  },
  template: `
<dialog ref="dlg" class="run-config-dialog" aria-labelledby="runConfigTitle" @cancel="cancel">
  <div class="dlg-head">
    <h3 id="runConfigTitle">运行回测</h3>
    <button type="button" class="dlg-close" @click="closeDlg" :disabled="submitting" aria-label="关闭运行配置">×</button>
  </div>
  <form novalidate @submit.prevent="confirm">
    <fieldset class="run-config-fields" :disabled="submitting">
      <p class="hint">初始资金 {{ account.initialCapital / 10000 }} 万元 · 最多 {{ account.maxPositions }} 只 · 每笔预算 {{ account.orderBudget / 10000 }} 万元（含费用）</p>
      <div class="run-config-years">
        <div v-for="y in years" :key="y.key" class="form-row">
          <label :for="'run-' + y.key">{{ y.label }}</label>
          <input :id="'run-' + y.key" type="number" step="1" v-model.number="cfg[y.key]" :aria-invalid="String(errorKey===y.key)" :aria-describedby="errorKey===y.key ? 'runConfigError' : undefined">
        </div>
      </div>
      <div class="form-row"><label for="run-sampleMode">股票范围</label>
        <select id="run-sampleMode" v-model="cfg.sampleMode" :aria-invalid="String(errorKey==='sampleMode')" :aria-describedby="errorKey==='sampleMode' ? 'runConfigError' : undefined">
          <option value="all">全部沪深主板</option><option value="random">随机样本</option><option value="codes">指定代码</option>
        </select>
      </div>
      <div v-if="cfg.sampleMode==='random'" class="form-row"><label for="run-sampleSize">样本数量</label>
        <input id="run-sampleSize" type="number" step="1" v-model.number="cfg.sampleSize" :aria-invalid="String(errorKey==='sampleSize')" :aria-describedby="errorKey==='sampleSize' ? 'runConfigError' : undefined">
      </div>
      <div v-if="cfg.sampleMode==='codes'" class="form-row"><label for="run-sampleCodes">股票代码</label>
        <input id="run-sampleCodes" type="text" v-model="cfg.sampleCodes" placeholder="sh600000,sz000001" :aria-invalid="String(errorKey==='sampleCodes')" :aria-describedby="errorKey==='sampleCodes' ? 'runConfigError' : undefined">
      </div>
      <template v-if="mode!=='builder'">
        <h4>卖出规则</h4>
        <div v-for="e in exits" :key="e.key" class="form-row run-config-exit">
          <label :for="'run-' + e.on"><input :id="'run-' + e.on" type="checkbox" v-model="cfg[e.on]" :aria-invalid="String(errorKey===e.on)" :aria-describedby="errorKey===e.on ? 'runConfigError' : undefined"> {{ e.label }}</label>
          <input :id="'run-' + e.key" type="number" :step="e.step" :disabled="!cfg[e.on]" v-model.number="cfg[e.key]" :aria-label="e.label + '（' + e.unit + '）'" :aria-invalid="String(errorKey===e.key)" :aria-describedby="errorKey===e.key ? 'runConfigError' : undefined">
          <span class="hint">{{ e.unit }}</span>
        </div>
      </template>
    </fieldset>
    <div v-if="error" id="runConfigError" class="msg err" role="alert">{{ error }}</div>
    <div class="run-config-actions">
      <button type="button" class="secondary" @click="closeDlg" :disabled="submitting">取消</button>
      <button type="submit" class="primary" :disabled="submitting" :aria-busy="String(submitting)">{{ submitting ? '提交中…' : '开始回测' }}</button>
    </div>
  </form>
</dialog>
  `,
};
