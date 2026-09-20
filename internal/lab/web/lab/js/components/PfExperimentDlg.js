// PfExperimentDlg：创建组合实验（运行）对话框（06 页“创建实验…”/模型行“实验”触发）。
// 迁移自 index.html openPfExperimentDlg/createPfExperiment。
// 由根组件 provide openPfExpDlg(model) 打开；保存成功后置 pfExpDirty 通知 06 页刷新。
import { state } from '../store.js';
import { Api } from '../api.js';
import { dlgMixin } from './dlgMixin.js';

export default {
  name: 'PfExperimentDlg',
  mixins: [dlgMixin],
  data() {
    return {
      model: null, // 目标模型（含 revision/hash/evidenceClass）
      studyStart: '',
      studyEnd: '',
      variantName: '主基线',
      variantSource: 'baseline',
      trialCounted: true,
      countReason: '',
      msg: {text: '', ok: true},
      saving: false,
    };
  },
  watch: {
    // 表单输入变化 → 重置幂等 requestId（同一次动作网络失败重试复用）
    studyStart() { state.pfExpRequestId = null; },
    studyEnd() { state.pfExpRequestId = null; },
    variantName() { state.pfExpRequestId = null; },
    variantSource() { state.pfExpRequestId = null; },
    trialCounted() { state.pfExpRequestId = null; },
    countReason() { state.pfExpRequestId = null; },
    open(v) { if (!v) { state.pfExpRequestId = null; this.msg = {text: '', ok: true}; } },
  },
  methods: {
    openDlg(model) {
      const m = model;
      if (!m || !m.modelId) return;
      this.model = m;
      this.variantName = '主基线';
      this.variantSource = 'baseline';
      this.trialCounted = true;
      this.countReason = m.evidenceClass === 'exploratory'
        ? '探索性模型，运行计入试验次数（结论不可作为正式组合验证）'
        : '主线基线运行，计入试验次数';
      this.msg = {text: '', ok: true};
      state.pfExpRequestId = null;
      this.open = true;
      this.$nextTick(() => this.$refs.studyStart && this.$refs.studyStart.focus());
    },
    async createExperiment() {
      const m = this.model;
      if (!m) return;
      const start = this.studyStart, end = this.studyEnd;
      if (!start || !end) { this.msg = {text: '请填写研究区间起止日期（YYYY-MM-DD）', ok: false}; return; }
      if (start > end) { this.msg = {text: '研究区间起始晚于结束', ok: false}; return; }
      const name = this.variantName.trim();
      if (!name) { this.msg = {text: '请填写变体名', ok: false}; return; }
      const reason = this.countReason.trim();
      if (!reason) { this.msg = {text: '请填写计数理由', ok: false}; return; }
      if (!state.pfExpRequestId) state.pfExpRequestId = crypto.randomUUID();
      const body = {
        requestId: state.pfExpRequestId,
        familyId: 'fam_' + m.modelId,
        modelId: m.modelId,
        modelRevision: m.revision,
        modelHash: m.modelHash,
        studyRange: {start, end},
        trainRange: {}, testRange: {},
        variant: {name, desc: ''},
        variantSource: this.variantSource,
        dataSnapshot: m.dataSnapshot || {},
        codeVersion: m.codeVersion || 'v2',
        evidenceClass: m.evidenceClass,
        trialCounted: this.trialCounted,
        countReason: reason,
      };
      this.saving = true;
      try {
        const d = await Api.createPortfolioExperiment(body);
        state.pfExpRequestId = null;
        this.msg = {text: d.created ? '已创建实验（queued），可在运行列表中启动。' : '幂等命中：该请求此前已创建。', ok: true};
        this.closeDlg();
        state.pfExpPage = 1;
        state.pfExpDirty++; // 通知 06 页刷新
      } catch (e) {
        // 服务端错误保留输入与 requestId（重试不重复创建）
        this.msg = {text: e.message, ok: false};
      } finally {
        this.saving = false;
      }
    },
  },
  template: `
<dialog ref="dlg" aria-labelledby="pfExpDlgTitle">
  <div class="dlg-head">
    <h3 id="pfExpDlgTitle">创建组合实验（运行）</h3>
    <button type="button" class="dlg-close" @click="closeDlg" aria-label="关闭">×</button>
  </div>
  <div class="candidate-form" style="margin-top:0">
    <div class="cf-row"><label for="pfExpModelRef">模型</label><span id="pfExpModelRef" style="font-size:12px;color:var(--ink2)">{{ model ? model.modelId + '（revision v' + model.revision + '）' : '—' }}</span></div>
    <div class="cf-row"><label for="pfStudyStart">研究区间起</label><input type="date" id="pfStudyStart" ref="studyStart" v-model="studyStart" aria-describedby="pfExpNote"></div>
    <div class="cf-row"><label for="pfStudyEnd">研究区间止</label><input type="date" id="pfStudyEnd" v-model="studyEnd" aria-describedby="pfExpNote"></div>
    <div class="cf-row"><label for="pfVariantName">变体名</label><input type="text" id="pfVariantName" v-model="variantName" maxlength="80" placeholder="如 主基线"></div>
    <div class="cf-row"><label for="pfVariantSource">变体来源</label>
      <select id="pfVariantSource" v-model="variantSource">
        <option value="baseline">baseline（基线）</option>
        <option value="declared">declared（预先声明变体）</option>
        <option value="exploratory">exploratory（探索性）</option>
      </select></div>
    <div class="cf-row"><label for="pfTrialCounted">计入试验次数</label><input type="checkbox" id="pfTrialCounted" v-model="trialCounted"></div>
    <div class="cf-row"><label for="pfCountReason">计数理由</label><textarea class="resize-none" id="pfCountReason" v-model="countReason" maxlength="500" placeholder="说明计入/不计入试验次数的理由"></textarea></div>
    <div class="hint" id="pfExpNote" style="margin:4px 0 10px">研究区间必填（YYYY-MM-DD）；创建成功返回 queued 状态，随后在运行列表中启动。</div>
    <div class="cf-row">
      <div class="cf-actions">
        <button type="button" class="primary" @click="createExperiment" :disabled="saving" :aria-busy="String(saving)">创建实验</button>
        <button type="button" class="secondary" @click="closeDlg">取消</button>
      </div>
    </div>
    <div class="msg" :class="msg.ok ? 'ok' : 'err'" role="status" aria-live="polite">{{ msg.text }}</div>
  </div>
</dialog>
  `,
};
