// Tab5Candidates：05 候选因子（列表 / 归档切换 / 加入策略）。
// 从 index.html 迁移：loadCandidates/toggleCandidatesArchived/renderCandidates/
// joinCandidateToStrategy/changeCandidateStatus。
// 证据/编辑对话框由根组件 provide（openCandidateEvidence / openCandidateEdit）。
// 加入策略复用 store.simpleConds + focusCondReq 跨页联动（与 01→02 同机制）。
import { watch } from '../vendor/vue.esm-browser.prod.js';
import { state } from '../store.js';
import { Api } from '../api.js';
import { factorTypeName, filterText, sig3 } from '../format.js';

export default {
  name: 'Tab5Candidates',
  inject: ['switchTab', 'openCandidateEvidence', 'openCandidateEdit'],
  setup() {
    return {state};
  },
  data() {
    return {
      loadErr: '',
      listHint: '', // 归档/恢复/失败的操作反馈
    };
  },
  computed: {
    archBtnText() { return state.showCandidatesArchived ? '只看活动' : '查看归档'; },
    emptyText() {
      return state.showCandidatesArchived
        ? '暂无候选因子（含归档）。'
        : '暂无候选因子：完成分析后可“保存为候选”。';
    },
    rows() {
      return state.candidates.map(c => {
        const fent = state.factorCatalog.find(x => x.kind === c.factor.kind);
        const fname = fent ? factorTypeName(fent) : c.factor.name;
        const ev = c.evidence || {};
        const cmp = c.compatibility || {};
        const useText = c.use && c.use.mode === 'range' ? filterText(c.use.filter, state.factorCatalog) : '仅观察';
        const canJoin = c.status === 'candidate' && c.use && c.use.mode === 'range' && cmp.state === 'ready';
        return {
          id: c.id,
          name: c.name || '（未命名）',
          factor: `${fname} · ${c.factor.days} 日 · 实现 v${c.factor.implementationVersion}`,
          use: useText,
          evidence: ev.firstDataDate && ev.lastDataDate ? `${ev.firstDataDate} ~ ${ev.lastDataDate}` : '—',
          status: c.status,
          cmpState: cmp.state,
          cmpText: cmp.state === 'ready' ? '可复用'
            : cmp.state === 'stale' ? '版本已变' : (cmp.state === 'missing' ? '已移除' : '—'),
          cmpNote: (cmp.state === 'stale' || cmp.state === 'missing')
            ? (cmp.reason || (cmp.state === 'stale' ? '请重新分析后再复用' : '注册表已无此因子')) : '',
          canJoin,
          raw: c,
        };
      });
    },
  },
  mounted() {
    this.loadCandidates();
    // 01 页保存成功后触发本页刷新
    watch(() => state.candidatesDirty, () => this.loadCandidates());
  },
  methods: {
    async loadCandidates() {
      this.loadErr = '';
      try {
        const d = await Api.candidates(state.showCandidatesArchived);
        state.candidates = d.candidates || [];
      } catch (e) {
        this.loadErr = '候选列表加载失败：' + e.message + '，可点击“刷新”重试。';
      }
    },
    toggleArchived() {
      state.showCandidatesArchived = !state.showCandidatesArchived;
      this.loadCandidates();
    },
    changeCandidateStatus(c, status) {
      this.changeStatus(c, status);
    },
    async changeStatus(c, status) {
      try {
        const body = {expectedRevision: c.revision, name: c.name, use: c.use, status,
          notes: c.notes || ''};
        await Api.updateCandidate(c.id, body);
        this.listHint = status === 'archived' ? '已归档（历史修订保留）' : '已恢复为候选';
        await this.loadCandidates();
      } catch (e) {
        this.listHint = '操作失败：' + e.message + '，已刷新到服务端最新。';
        await this.loadCandidates();
      }
    },
    // 把 range+ready+candidate 映射进 simpleConds 并写 factorVersion；
    // 相同 kind+days 不静默重复，聚焦原条件并明确要求替换；加入后切到 02 页，
    // 聚焦并标注“尚未回测”，不自动运行回测。
    joinCandidateToStrategy(c) {
      const f = c.use.filter;
      const ent = state.factorCatalog.find(x => x.kind === f.kind);
      const unit = ent ? ent.unit : '';
      const toDisp = v => v == null ? null : (unit === 'ratio' ? +(v * 100).toFixed(4) : v);
      const cond = {
        kind: f.kind,
        days: f.days,
        operator: f.operator,
        min: toDisp(f.min),
        max: toDisp(f.max),
        factorVersion: f.factorVersion || c.factor.implementationVersion,
        hl: false, err: '', errField: '', nb: true, // nb=尚未回测标注
      };
      const dupIdx = state.simpleConds.findIndex(x => x.kind === f.kind && x.days === f.days);
      if (dupIdx >= 0) {
        this.switchTab(1);
        state.focusCondReq = {kind: f.kind, days: f.days};
        if (!window.confirm(`已存在相同因子+天数条件（第 ${dupIdx + 1} 条），是否用候选阈值替换？`)) {
          state.scriptMsg = {text: '未替换：已聚焦原条件，请自行确认阈值。', ok: true};
          return;
        }
        state.simpleConds[dupIdx] = cond;
        state.scriptMsg = {text: '候选阈值已替换原条件（尚未回测），请运行回测。', ok: true};
        return;
      }
      state.simpleConds.push(cond);
      this.switchTab(1);
      state.focusCondReq = {kind: f.kind, days: f.days};
      state.scriptMsg = {text: '候选已加入策略条件（尚未回测），填写卖出规则后运行回测。', ok: true};
    },
  },
  template: `
<div class="card" id="candidateSection" style="margin-top:18px">
  <h3>我的候选因子</h3>
  <div class="cand-toolbar">
    <button class="secondary" @click="toggleArchived" :aria-pressed="String(state.showCandidatesArchived)">{{ archBtnText }}</button>
    <button class="secondary" @click="loadCandidates">刷新</button>
    <span class="hint" style="margin:0" id="candListHint">候选保存的是不可变分析证据；保存不代表样本外验证通过。</span>
  </div>
  <div class="warnbar" v-if="loadErr" id="candLoadErr" role="alert">{{ loadErr }}</div>
  <template v-if="rows.length">
    <div class="cand-table-wrap" id="candTableWrap">
      <table id="candTable">
        <thead><tr><th>名称</th><th>因子</th><th>用途</th><th>证据区间</th><th>状态</th><th>兼容</th><th>操作</th></tr></thead>
        <tbody id="candBody">
          <tr v-for="r in rows" :key="r.id">
            <td>{{ r.name }}</td>
            <td>{{ r.factor }}</td>
            <td>{{ r.use }}</td>
            <td>{{ r.evidence }}</td>
            <td><span class="st-chip" :class="r.status === 'archived' ? 'archived' : 'candidate'">{{ r.status === 'archived' ? '已归档' : '候选' }}</span></td>
            <td>
              <span class="st-chip" :class="r.cmpState || ''">{{ r.cmpText }}</span>
              <div class="compat-note" v-if="r.cmpNote">{{ r.cmpNote }}</div>
            </td>
            <td class="cand-op">
              <button type="button" @click="openCandidateEvidence(r.raw)">证据</button>
              <button type="button" @click="openCandidateEdit(r.raw)">编辑</button>
              <button type="button" class="join" v-if="r.canJoin" @click="joinCandidateToStrategy(r.raw)">加入策略</button>
              <button type="button" class="arch" v-if="r.status === 'candidate'" @click="changeCandidateStatus(r.raw, 'archived')">归档</button>
              <button type="button" class="restore" v-else @click="changeCandidateStatus(r.raw, 'candidate')">恢复</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </template>
  <div class="empty" v-else id="candEmpty" role="status">{{ emptyText }}</div>
  <div class="hint" v-if="listHint" style="margin-top:8px">{{ listHint }}</div>
</div>
  `,
};
