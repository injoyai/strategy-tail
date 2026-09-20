// PfValidationDlg：创建样本外验证对话框（06 页“创建验证…”/模型行“验证”触发）。
// 迁移自 index.html openPfValidationDlg/createPfValidation/pfGatesFromForm。
// 验证规格（窗口/门禁）使用 06 页“⑥ 门禁”分区当前值：主组件 provide getPfGates()。
// 由根组件 provide openPfValDlg(model) 打开；保存成功后置 pfValDirty 通知 06 页刷新。
import { state } from '../store.js';
import { Api } from '../api.js';
import { dlgMixin } from './dlgMixin.js';

export default {
  name: 'PfValidationDlg',
  mixins: [dlgMixin],
  inject: ['getPfGates'],
  data() {
    return {
      model: null,
      msg: {text: '', ok: true},
      saving: false,
    };
  },
  watch: {
    open(v) { if (!v) { state.pfValRequestId = null; this.msg = {text: '', ok: true}; } },
  },
  methods: {
    openDlg(model) {
      const m = model;
      if (!m || !m.modelId) return;
      this.model = m;
      this.msg = {text: '', ok: true};
      state.pfValRequestId = null;
      this.open = true;
      this.$nextTick(() => this.$refs.createBtn && this.$refs.createBtn.focus());
    },
    async createValidation() {
      const m = this.model;
      if (!m) return;
      const gates = this.getPfGates ? this.getPfGates() : {};
      const trainDays = parseInt(gates._trainDays, 10) || 0;
      const testDays = parseInt(gates._testDays, 10) || 0;
      const step = parseInt(gates._step, 10) || 0;
      if (!(trainDays >= 1 && trainDays <= 10000)) { this.msg = {text: '训练窗天数应为 1-10000', ok: false}; return; }
      if (!(testDays >= 1 && testDays <= 10000)) { this.msg = {text: '测试窗天数应为 1-10000', ok: false}; return; }
      if (step < testDays || step > 10000) { this.msg = {text: '步长应为 >= 测试窗天数（且 <=10000）', ok: false}; return; }
      if (!state.pfValRequestId) state.pfValRequestId = crypto.randomUUID();
      const body = {
        requestId: state.pfValRequestId,
        spec: {
          modelRef: {modelId: m.modelId, revision: m.revision, hash: m.modelHash},
          windowRule: {trainDays, testDays, step},
          gates: {
            minValidWindows: gates.minValidWindows || 0,
            minTradingDays: gates.minTradingDays || 0,
            minNetReturn: gates.minNetReturn || 0,
            maxDrawdown: gates.maxDrawdown || 0,
            maxCostDrag: gates.maxCostDrag || 0,
            minInformationRatio: gates.minIR || 0,
            minExcessStability: gates.minExcessStability || 0,
            maxTurnover: gates.maxTurnoverGate || 0,
            maxCashResidual: gates.maxCashResidual || 0,
            maxUnfilledRate: gates.maxUnfilled || 0,
            maxConcentration: gates.maxConcentration || 0,
            minDirectionConsistency: gates.minDirectionConsistency || 0,
            minBaselineIncrement: gates.minBaselineIncrement || 0,
            maxDegradedWindows: gates.maxDegradedWindows || 0,
          },
          benchmark: {},
          evidenceRequirement: '',
        },
      };
      this.saving = true;
      try {
        const d = await Api.createPortfolioValidation(body);
        state.pfValRequestId = null;
        this.msg = {text: d.created ? '已创建验证（规格已冻结），可在验证列表中启动。' : '幂等命中：该请求此前已创建。', ok: true};
        this.closeDlg();
        state.pfValPage = 1;
        state.pfValDirty++; // 通知 06 页刷新
      } catch (e) {
        this.msg = {text: e.message, ok: false};
      } finally {
        this.saving = false;
      }
    },
  },
  template: `
<dialog ref="dlg" aria-labelledby="pfValDlgTitle">
  <div class="dlg-head">
    <h3 id="pfValDlgTitle">创建样本外验证</h3>
    <button type="button" class="dlg-close" @click="closeDlg" aria-label="关闭">×</button>
  </div>
  <div class="candidate-form" style="margin-top:0">
    <div class="cf-row"><label for="pfValModelRef">模型</label><span id="pfValModelRef" style="font-size:12px;color:var(--ink2)">{{ model ? model.modelId + '（revision v' + model.revision + '）' : '—' }}</span></div>
    <div class="hint" style="margin:0 0 10px">验证规格（窗口/门禁/基准）使用上方“⑥ 门禁”分区当前值；规格全部字段进入验证 hash，修改模型或门禁即产生新验证 ID。旧验证保留，并由新验证的 Supersedes 标记为已见数据。</div>
    <div class="cf-row">
      <div class="cf-actions">
        <button type="button" class="primary" ref="createBtn" @click="createValidation" :disabled="saving" :aria-busy="String(saving)">创建验证</button>
        <button type="button" class="secondary" @click="closeDlg">取消</button>
      </div>
    </div>
    <div class="msg" :class="msg.ok ? 'ok' : 'err'" role="status" aria-live="polite">{{ msg.text }}</div>
  </div>
</dialog>
  `,
};
