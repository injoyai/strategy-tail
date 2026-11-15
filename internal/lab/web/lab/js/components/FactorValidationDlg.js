import { Api, requestUUID } from '../api.js';
import { state } from '../store.js';
import { dlgMixin } from './dlgMixin.js';

const labels = {frozen: '待运行', running: '运行中', passed: '通过', failed: '未通过', insufficient: '数据不足', error: '执行失败'};

export default {
  name: 'FactorValidationDlg',
  mixins: [dlgMixin],
  data() {
    return {
      candidate: null, inputs: null, items: [], view: null, selected: '', sequence: 0,
      mode: 'prospective', direction: '', from: '', to: '',
      minCoverage: 80, minWindows: 1, directionRate: 70,
      requestId: '', pending: false, loading: false, error: '', inputError: '',
    };
  },
  computed: {
    labels() { return labels; },
    running() { return state.taskRunning && state.status?.runId === this.selected; },
  },
  methods: {
    async openDlg(candidate) {
      const seq = ++this.sequence;
      Object.assign(this, {candidate, inputs: null, items: [], view: null, selected: '',
        mode: 'prospective', direction: '', from: '', to: '', minCoverage: 80, minWindows: 1,
        directionRate: 70, error: '', inputError: '', requestId: '', pending: false, loading: true, open: true});
      try {
        const [list, inputs] = await Promise.allSettled([
          Api.factorValidations({candidateId: candidate.id}), Api.factorValidationInputs(candidate.id),
        ]);
        if (seq !== this.sequence || !this.open) return;
        if (list.status === 'fulfilled') {
          this.items = list.value.validations || [];
          if (this.items.length) this.select(this.items[0].id);
        } else this.error = list.reason.message;
        if (inputs.status === 'fulfilled') {
          this.inputs = inputs.value;
          this.direction = ['positive', 'negative'].includes(this.inputs.direction) ? this.inputs.direction : '';
        } else this.inputError = inputs.reason.message;
      } catch (e) {
        if (seq === this.sequence) this.error = e.message;
      } finally {
        if (seq === this.sequence) this.loading = false;
      }
    },
    async select(id) {
      this.selected = id;
      this.view = null;
      const seq = this.sequence;
      if (!id) return;
      try {
        const data = await Api.factorValidation(id);
        if (seq === this.sequence && id === this.selected && this.open) this.view = data;
      } catch (e) {
        if (seq === this.sequence && id === this.selected && this.open) this.error = e.message;
      }
    },
    async freeze() {
      if (!this.inputs || this.pending) return;
      this.error = '';
      const field = !this.direction ? 'direction' : !this.from || this.from <= this.inputs.lastSignalDate ? 'from'
        : !this.to || this.to < this.from ? 'to' : '';
      if (field) {
        this.error = {direction: '请选择预期方向。', from: '测试开始日必须晚于发现期最后信号日。', to: '测试结束日必须等于或晚于开始日。'}[field];
        this.$refs[field]?.focus();
        return;
      }
      if (!Number.isInteger(this.minWindows) || this.minWindows < 1 ||
          !Number.isFinite(this.minCoverage) || this.minCoverage < 0 || this.minCoverage > 100 ||
          !Number.isFinite(this.directionRate) || this.directionRate < 0 || this.directionRate > 100) {
        this.error = '覆盖率与同向比例应在 0% 至 100% 之间，窗口数须为正整数。';
        return;
      }
      this.pending = true;
      const seq = this.sequence;
      this.requestId ||= requestUUID();
      try {
        const d = await Api.createFactorValidation({
          requestId: this.requestId, candidateId: this.candidate.id, candidateRevision: this.inputs.candidateRevision,
          evidenceClass: this.mode, discoveryAnalysisIds: [this.inputs.analysisId], trialIds: this.inputs.trialIds,
          direction: this.direction, testRange: {start: this.from, end: this.to},
          windows: {trainYears: 1, testYears: 1, stepYears: 1, purgeDays: this.inputs.purgeDays},
          policy: {minCoverage: this.minCoverage / 100, minValidWindows: this.minWindows,
            requiredDirectionRate: this.directionRate / 100, requireTradableLabel: true},
        });
        if (seq !== this.sequence || !this.open) return;
        this.requestId = '';
        if (!this.items.some(x => x.id === d.validation.id)) this.items.unshift({id: d.validation.id, state: 'frozen'});
        await this.select(d.validation.id);
      } catch (e) {
        if (seq === this.sequence) this.error = e.message;
      } finally {
        if (seq === this.sequence) this.pending = false;
      }
    },
    async run() {
      if (!this.selected || this.pending || state.taskRunning) return;
      const seq = this.sequence;
      this.pending = true;
      this.error = '';
      try {
        await Api.runFactorValidation(this.selected);
        this.startTask();
      } catch (e) {
        if (seq === this.sequence) this.error = e.message;
      } finally {
        if (seq === this.sequence) this.pending = false;
      }
    },
    onTaskDone(id) {
      if (!this.open || this.selected !== id) return;
      this.select(id).then(() => {
        const item = this.items.find(x => x.id === id);
        if (item && this.view?.request.id === id) item.state = this.view.state;
      });
    },
    percent(v) { return Number.isFinite(v) ? (v * 100).toFixed(2) + '%' : '—'; },
  },
  inject: ['startTask'],
  template: `
<dialog ref="dlg" aria-labelledby="factorValidationTitle">
  <div class="dlg-head">
    <h3 id="factorValidationTitle">单因子验证</h3>
    <button type="button" class="dlg-close" @click="closeDlg" aria-label="关闭">×</button>
  </div>
  <div class="candidate-form">
    <p v-if="candidate" class="hint">{{ candidate.name }} · 候选 v{{ inputs?.candidateRevision || candidate.revision }}</p>
    <p v-if="loading" class="hint" role="status">正在读取验证记录…</p>
    <div v-if="items.length" class="cf-row">
      <label for="fvExisting">验证记录</label>
      <select id="fvExisting" :value="selected" @change="select($event.target.value)">
        <option v-for="item in items" :key="item.id" :value="item.id">{{ labels[item.state] }} · {{ item.frozenAt ? item.frozenAt.slice(0, 10) : item.id }}</option>
      </select>
    </div>
    <div v-if="view" class="cf-row" role="status">
      <span>{{ labels[view.state] }} <template v-if="view.request.protocol.testRange"> · {{ view.request.protocol.testRange.start }} 至 {{ view.request.protocol.testRange.end }}</template></span>
      <button v-if="view.state === 'frozen' && view.request.protocol.testRange" type="button" class="primary" :disabled="pending || state.taskRunning" @click="run">{{ running ? '运行中' : '运行验证' }}</button>
    </div>
    <div v-if="view?.report">
      <div v-for="check in view.report.checks" :key="check.name" class="hint">{{ check.name }} {{ check.actual }}（门槛 {{ check.expected }}）</div>
      <details v-for="window in view.windows" :key="window.index">
        <summary>{{ window.startDate }} 至 {{ window.endDate }} · {{ window.observations }} 个有效 IC 日</summary>
        <p v-if="window.message" class="hint">{{ window.message }}</p>
        <p v-if="window.missingCodeYears" class="hint">缺行情 {{ window.missingCodeYears }} 个股票年份</p>
        <div v-for="h in window.horizons" :key="h.horizon" class="hint">
          {{ h.horizon }} 日 · Rank IC {{ h.ic == null ? '—' : h.ic.toFixed(4) }} · 覆盖 {{ percent(h.coverage) }} · 头尾差 {{ percent(h.spread) }} · 领先同池等权 {{ percent(h.topExcess) }}
        </div>
      </details>
      <p class="hint">收益为价格标签毛收益。行业、中性化及费用口径见报告说明。</p>
    </div>
    <p v-if="inputError" class="hint">{{ inputError }}</p>
    <form v-if="inputs" novalidate @input="requestId = ''" @submit.prevent="freeze">
      <fieldset :disabled="pending">
        <legend>冻结新验证</legend>
        <div class="cf-row"><label for="fvMode">方式</label><select id="fvMode" v-model="mode"><option value="prospective">平台内前瞻</option><option v-if="inputs.allowsRetrospective" value="retrospective">历史独立区间</option></select></div>
        <div class="cf-row"><label for="fvDirection">预期方向</label><select id="fvDirection" ref="direction" v-model="direction" required><option value="" disabled>请选择</option><option value="positive">因子高值领先</option><option value="negative">因子低值领先</option></select></div>
        <div class="cf-row"><label for="fvStart">测试开始</label><input id="fvStart" ref="from" v-model="from" type="date" required aria-describedby="fvDatesNote"></div>
        <div class="cf-row"><label for="fvEnd">测试结束</label><input id="fvEnd" ref="to" v-model="to" type="date" :min="from" required aria-describedby="fvDatesNote"></div>
        <div class="cf-row"><label for="fvCoverage">最低标签覆盖率（%）</label><input id="fvCoverage" v-model.number="minCoverage" type="number" min="0" max="100" step="any"></div>
        <div class="cf-row"><label for="fvWindows">最少有效窗口</label><input id="fvWindows" v-model.number="minWindows" type="number" min="1" step="1"></div>
        <div class="cf-row"><label for="fvDirectionRate">最低同向窗口比例（%）</label><input id="fvDirectionRate" v-model.number="directionRate" type="number" min="0" max="100" step="any"></div>
        <p id="fvDatesNote" class="hint">发现期最后信号日 {{ inputs.lastSignalDate }}。前瞻测试从冻结日之后开始，测试区间结束并有行情后可运行。窗口按年推进，边界隔离 {{ inputs.purgeDays }} 个交易日。</p>
        <div class="cf-actions"><button type="submit" class="primary" :aria-busy="String(pending)">冻结验证</button></div>
      </fieldset>
    </form>
    <p v-if="error" class="warnbar" role="alert">{{ error }}</p>
  </div>
</dialog>
  `,
  setup() { return {state}; },
};
