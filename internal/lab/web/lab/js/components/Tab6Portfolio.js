// Tab6Portfolio：06 组合研究（证据脊柱 / 模型列表 / 模型编辑 / 实验与验证列表 / 运行与验证详情）。
// 契约：页面只渲染后端返回值（verdict/指标/归因），不在浏览器重算正式结论。
// 迁移自 index.html 3031-4654 全部 pf* 逻辑；状态全部在 store（上 session 已建）。
// 创建实验/验证对话框由根组件 provide openPfExpDlg/openPfValDlg/getPfGates。
// 归因/指标/窗口/门禁等无按钮区使用 v-html（内部已 esc），状态行与操作按钮用模板渲染。
import { watch } from '../vendor/vue.esm-browser.prod.js';
import { state } from '../store.js';
import { Api } from '../api.js';
import { esc, pfPct, pfNum, pfCls, pfShort, pfErrMsg, factorInstanceName } from '../format.js';
import { mountChart, dropChart, NAV_COLORS } from '../echarts.js';

const PF_EVIDENCE = {exploratory: '探索性', retrospective: '回溯', prospective: '前瞻'};
const PF_DIR = {higher_is_better: '越高越好', lower_is_better: '越低越好'};
const PF_VERDICT = {passed: '通过', failed: '未通过', insufficient: '证据不足', error: '执行错误'};
const PF_RUNSTATE = {queued: '排队', running: '运行中', completed: '完成', failed: '失败', cancelled: '已取消', insufficient: '证据不足'};
const PF_GATE = {pass: '通过', fail: '未通过', unknown: '未配置'};
const PF_VAL_STATE = {created: '进行中', completed: '已完成'};

export default {
  name: 'Tab6Portfolio',
  inject: ['switchTab', 'startTask', 'openPfExpDlg', 'openPfValDlg'],
  setup() {
    return {state, PF_RUNSTATE, PF_VERDICT};
  },
  data() {
    return {
      pfInitialized: false,
      editOpen: false,        // 模型编辑卡可见
      editMsg: {text: '', ok: true},
      editStaleText: '',
      runCardOpen: false,
      valCardOpen: false,
      runLoadingText: '',
      runErr: '',
      expErr: '',
      valErr: '',
      valDetailErr: '',
      navC: null,
      // 模型编辑表单（⑥ 门禁分区同时是“创建验证”的规格来源）
      form: {
        createdBy: '', researchQuestion: '', hypothesis: '',
        missing: 'exclude', winsorize: 'none', winsorizeParam: '', neutralize: 'none',
        industryDataset: '', sizeDataset: '', standardize: 'rank',
        combineMethod: 'equal_weight_rank', icWindow: 3, icShrink: 0.5, icMax: 0.5, icFallback: 'equal_weight',
        selection: 'top_n', topN: 20, topQuantile: 0.2,
        maxStockWeight: 0, maxIndustryWeight: 0, maxTurnover: 0, minHoldings: 5, cashBuffer: 0,
        rebalance: 'daily', fillAt: 'next_open', sellFirst: true, t1: true, carryUnfilled: false,
        lotSize: 100, commission: 0.0003, stampDuty: 0.001, transferFee: 0, slippage: 0.01, minCommission: 5,
        trainDays: '', testDays: '', step: '',
        minValidWindows: 0, minTradingDays: 0, minNetReturn: '', maxDrawdown: '', maxCostDrag: '',
        minIR: '', minExcessStability: '', maxTurnoverGate: '', maxCashResidual: '', maxUnfilled: '',
        maxConcentration: '', minDirectionConsistency: '', minBaselineIncrement: '', maxDegradedWindows: 0,
      },
    };
  },
  computed: {
    // —— 表单显隐 ——
    winsorizeRowVisible() { return this.form.winsorize === 'quantile' || this.form.winsorize === 'mad'; },
    winsorizeLabel() { return this.form.winsorize === 'mad' ? 'MAD 倍数（0-100）' : '分位阈值（双侧 0-0.5）'; },
    neutralizeRowVisible() { return this.form.neutralize === 'industry_size'; },
    combineRowVisible() { return this.form.combineMethod === 'rolling_ic_weight'; },
    topNVisible() { return this.form.selection === 'top_n'; },
    topQuantileVisible() { return this.form.selection === 'top_quantile'; },
    editTitle() {
      const m = state.pfEditModel;
      return m && m.modelId
        ? `模型编辑 · ${m.modelId}（当前 revision v${m.revision}）`
        : '创建新模型';
    },
    // —— 模型列表 ——
    modelRows() { return this.filteredSortedModels(); },
    modelEmptyText() {
      const pg = state.pfModelPageData;
      const total = pg ? (pg.total || 0) : 0;
      if (!total) return '暂无模型：完成 v1 验证后点击“创建模型”，把已验证因子组装成冻结模型。';
      if (!this.modelRows.length) {
        return state.pfModelPage > this.modelPages
          ? `页码越界：共 ${total} 个模型，当前在第 ${state.pfModelPage} 页之外，请翻页返回。`
          : '本页没有匹配筛选条件的模型。';
      }
      return '';
    },
    modelPages() {
      const pg = state.pfModelPageData;
      return Math.max(1, Math.ceil((pg ? (pg.total || 0) : 0) / (pg && pg.pageSize ? pg.pageSize : state.pfModelPageSize)));
    },
    modelPageInfo() {
      const pg = state.pfModelPageData || {};
      return `第 ${pg.page || state.pfModelPage}/${this.modelPages} 页 · 共 ${pg.total || 0} 个模型`;
    },
    modelHint() {
      return this.modelRows.length
        ? '状态列 = 该模型最近一条验证结论（后端 verdict）；模型列表为服务端分页，ID/状态筛选与排序在当前页内进行。'
        : '';
    },
    // —— 运行 / 验证列表 ——
    expItems() { return (state.pfExperiments && state.pfExperiments.items) || []; },
    expPages() {
      const pg = state.pfExperiments || {};
      return Math.max(1, Math.ceil((pg.total || 0) / (pg.pageSize || state.pfExpPageSize)));
    },
    expPageInfo() {
      const pg = state.pfExperiments || {};
      return `第 ${pg.page || 1}/${this.expPages} 页 · 共 ${pg.total || 0} 条`;
    },
    valItems() { return (state.pfValidations && state.pfValidations.items) || []; },
    valPages() {
      const pg = state.pfValidations || {};
      return Math.max(1, Math.ceil((pg.total || 0) / (pg.pageSize || state.pfValPageSize)));
    },
    valPageInfo() {
      const pg = state.pfValidations || {};
      return `第 ${pg.page || 1}/${this.valPages} 页 · 共 ${pg.total || 0} 条`;
    },
    // —— 运行详情 ——
    selectedRun() { return state.pfSelectedRun; },
    selectedRunReport() { return state.pfSelectedRunReport; },
    selectedRunStatus() { return (state.pfSelectedRun && state.pfSelectedRun.status) || ''; },
    selectedRunId() { return state.pfSelectedRun ? state.pfSelectedRun.experimentId : ''; },
    selectedRunMessage() { return state.pfSelectedRun ? state.pfSelectedRun.message : ''; },
    selectedRunError() { return state.pfSelectedRun ? state.pfSelectedRun.error : ''; },
    runStateCls() {
      const st = this.selectedRunStatus;
      return st === 'completed' ? 'ready' : (st === 'failed' || st === 'cancelled') ? 'missing' : st === 'insufficient' ? 'stale' : '';
    },
    runStateText() { return PF_RUNSTATE[this.selectedRunStatus] || this.selectedRunStatus || '—'; },
    runMetricsHtml() {
      const rep = this.selectedRunReport;
      if (!rep) return '';
      const m = rep.metrics || {};
      const g = m.gross || {}, n = m.net || {};
      const rrc = (r, tag) => [
        this.pfMetric(`累计收益（${tag}）`, pfPct(r.cumulativeReturn), pfCls(r.cumulativeReturn)),
        this.pfMetric(`年化收益（${tag}）`, pfPct(r.annualReturn), pfCls(r.annualReturn)),
        this.pfMetric(`年化波动（${tag}）`, pfPct(r.annualVolatility)),
        this.pfMetric(`Sharpe（${tag}）`, r.sharpe == null ? '—' : pfNum(r.sharpe, 2), '', r.sharpe == null ? (r.sharpeReason || '未定义') : ''),
        this.pfMetric(`Sortino（${tag}）`, r.sortino == null ? '—' : pfNum(r.sortino, 2), '', r.sortino == null ? (r.sortinoReason || '未定义') : ''),
        this.pfMetric(`最大回撤（${tag}）`, pfPct(r.maxDrawdown), 'neg'),
        this.pfMetric(`回撤持续（${tag}）`, (r.maxDrawdownDuration == null ? '—' : r.maxDrawdownDuration + ' 日')),
        this.pfMetric(`Calmar（${tag}）`, r.calmar == null ? '—' : pfNum(r.calmar, 2), '', r.calmar == null ? (r.calmarReason || '未定义') : ''),
      ].join('');
      return rrc(g, '毛') + rrc(n, '净') + this.pfMetric('有效收益样本日', m.tradingDays ? m.tradingDays + ' 日' : '—');
    },
    runQualityHtml() {
      const rep = this.selectedRunReport;
      if (!rep) return '';
      const q = (rep.metrics || {}).quality || {};
      const bm = (rep.metrics || {}).benchmark || {};
      const unf = q.unfilled || {};
      const reasons = Object.entries(unf.byReason || {}).map(([k, v]) => `${k}×${v}`).join('、');
      return [
        this.pfMetric('日换手均值', pfPct(q.dailyTurnover)),
        this.pfMetric('年换手', pfPct(q.annualTurnover)),
        this.pfMetric('成本拖累', pfPct(q.costDrag), q.costDrag > 0 ? 'neg' : ''),
        this.pfMetric('平均现金比例', pfPct(q.avgCashRatio)),
        this.pfMetric('平均持仓数', q.avgHoldings == null ? '—' : pfNum(q.avgHoldings, 0) + ' 只'),
        this.pfMetric('单票最大权重', pfPct(q.maxConcentration)),
        this.pfMetric('平均 HHI', pfNum(q.avgHhi, 4)),
        this.pfMetric('目标偏离均值', pfPct(q.targetDeviation && q.targetDeviation.mean)),
        this.pfMetric('目标偏离最大', pfPct(q.targetDeviation && q.targetDeviation.max)),
        this.pfMetric('未成交', unf.count == null ? '—' : unf.count + ' 笔', '', reasons || ''),
        this.pfMetric('基准', bm.available ? '可用' : '不可用', '', bm.unavailableReason || ''),
      ].join('');
    },
    runLimitations() { return (this.selectedRunReport && this.selectedRunReport.limitations || []).join('；'); },
    weightRows() {
      const rep = this.selectedRunReport;
      if (!rep) return [];
      const ws = (rep.attribution || {}).weightSeries || [];
      if (!ws.length) return [];
      const last = ws[ws.length - 1];
      const target = last.target || {}, actual = last.actual || {}, dev = last.deviation || {};
      const codes = [...new Set([...Object.keys(target), ...Object.keys(actual)])].sort();
      return codes.map(code => {
        const t = target[code] || 0, a = actual[code] || 0, dv = dev[code] != null ? dev[code] : (a - t);
        return {code, t, a, dv};
      });
    },
    attributionHtml() {
      const rep = this.selectedRunReport;
      if (!rep) return '';
      const att = rep.attribution || {};
      const rd = att.redundancy || {};
      const parts = [];
      parts.push(this.pfH4('因子相关（两两日截面 Spearman，时间均值/分位）'));
      parts.push(this.pfTable(['因子 A', '因子 B', '均值', 'P25', 'P50', 'P75', '有效日'],
        (rd.pairs || []).map(p => `<tr><td>${esc(p.factorA)}</td><td>${esc(p.factorB)}</td><td class="pf-num">${pfNum(p.mean, 3)}</td><td class="pf-num">${pfNum(p.p25, 3)}</td><td class="pf-num">${pfNum(p.p50, 3)}</td><td class="pf-num">${pfNum(p.p75, 3)}</td><td class="pf-num">${p.validDays}</td></tr>`)));
      parts.push(this.pfH4('覆盖重叠（有效股票交集/并集/重叠比例）'));
      parts.push(this.pfTable(['因子 A', '因子 B', '交集', '并集', '重叠', '有效日'],
        (rd.coverage || []).map(c => `<tr><td>${esc(c.factorA)}</td><td>${esc(c.factorB)}</td><td class="pf-num">${pfNum(c.meanIntersection, 2)}</td><td class="pf-num">${pfNum(c.meanUnion, 2)}</td><td class="pf-num">${pfNum(c.meanOverlap, 4)}</td><td class="pf-num">${c.validDays}</td></tr>`)));
      const singleIc = rd.singleIc || [], marginalIc = rd.marginalIc || [];
      const icRows = [...new Set([...singleIc.map(x => x.factor), ...marginalIc.map(x => x.factor)])].map(f => {
        const s = singleIc.find(x => x.factor === f), m = marginalIc.find(x => x.factor === f);
        return `<tr><td>${esc(f)}</td><td class="pf-num">${s ? pfNum(s.mean, 4) : '—'}</td><td class="pf-num">${s ? s.validDays : '—'}</td><td class="pf-num">${m ? pfNum(m.mean, 4) : '—'}</td><td class="pf-num">${m ? m.validDays : '—'}</td></tr>`;
      });
      parts.push(this.pfH4('单因子 IC / 边际 IC（控制其余因子后）'));
      if (rd.compositeIc) {
        parts.push(`<div class="hint" style="margin:6px 0">等权合成分数 IC 均值：<b class="pf-num">${pfNum(rd.compositeIc.mean, 4)}</b>（有效日 ${rd.compositeIc.validDays}）。</div>`);
      }
      parts.push(this.pfTable(['因子', '单因子 IC 均值', '有效日', '边际 IC 均值', '有效日'], icRows));
      parts.push(this.pfH4('留一法（移除后 IC 变化；正 = 移除后提升）'));
      parts.push(this.pfTable(['移除因子', '全因子 IC', '留一 IC', 'IC 变化', '有效日'],
        (rd.leaveOneOut || []).map(x => `<tr><td>${esc(x.removedFactor)}</td><td class="pf-num">${pfNum(x.fullIc, 4)}</td><td class="pf-num">${pfNum(x.looIc, 4)}</td><td class="pf-num ${pfCls(x.icChange)}">${pfNum(x.icChange, 4)}</td><td class="pf-num">${x.validDays}</td></tr>`)));
      if (rd.weightStability && rd.weightStability.length) {
        parts.push(this.pfH4('滚动权重稳定度'));
        parts.push(this.pfTable(['因子', '平均权重', '标准差', '触顶次数', '快照数'],
          rd.weightStability.map(w => `<tr><td>${esc(w.factor)}</td><td class="pf-num">${pfNum(w.meanWeight, 4)}</td><td class="pf-num">${pfNum(w.stdWeight, 4)}</td><td class="pf-num">${w.capHits}</td><td class="pf-num">${w.snapshots}</td></tr>`)));
      }
      (rd.warnings || []).forEach(w => parts.push(`<div class="hint" style="color:#f0cd86">⚠ ${esc(w)}</div>`));
      parts.push(this.pfH4('股票贡献（算术累计）'));
      parts.push(this.pfTable(['代码', '贡献'], (att.stocks || []).map(s => `<tr><td>${esc(s.code)}</td><td class="pf-num ${pfCls(s.contribution)}">${pfPct(s.contribution, 4)}</td></tr>`)));
      parts.push(this.pfH4('行业贡献'));
      parts.push(this.pfTable(['行业', '贡献', '股票数'], (att.industries || []).map(i => `<tr><td>${esc(i.industry)}</td><td class="pf-num ${pfCls(i.contribution)}">${pfPct(i.contribution, 4)}</td><td class="pf-num">${i.stockCount}</td></tr>`)));
      parts.push(this.pfH4('成本与偏离'));
      const c = att.cashContribution, cd = att.costDrag, nr = att.netReturn;
      parts.push('<div class="pf-metric-grid" style="margin-top:8px">' + [
        this.pfMetric('现金贡献', pfPct(c, 4), pfCls(c)),
        this.pfMetric('股票+现金合计', pfPct(att.stockCashSum, 4), pfCls(att.stockCashSum)),
        this.pfMetric('成本拖累', pfPct(cd, 4), cd > 0 ? 'neg' : ''),
        this.pfMetric('归因净收益（算术）', pfPct(nr, 4), pfCls(nr)),
        this.pfMetric('信号选择收益', att.selectionReturn == null ? '—' : pfPct(att.selectionReturn, 4), pfCls(att.selectionReturn)),
        this.pfMetric('执行偏离', att.executionDeviation == null ? '—' : pfPct(att.executionDeviation, 4), pfCls(att.executionDeviation)),
        this.pfMetric('净值口径净累计（复利）', att.portfolioNetReturn == null ? '—' : pfPct(att.portfolioNetReturn, 4), pfCls(att.portfolioNetReturn)),
        this.pfMetric('残差（披露）', att.residual == null ? '—' : pfPct(att.residual, 4), pfCls(att.residual)),
      ].join('') + '</div>');
      parts.push(`<div class="hint" style="margin-top:8px">${esc(att.limitation || '')}${att.reconciled ? '' : '（归因未对账）'}</div>`);
      return parts.join('');
    },
    artifactsHtml() {
      const exp = this.selectedRun;
      if (!exp) return '';
      const names = ['report.json', 'nav.csv', 'orders.csv', 'trades.csv', 'holdings.csv'];
      const base = '/api/portfolio-experiments/' + encodeURIComponent(exp.experimentId) + '/artifacts/';
      return names.map(n => `<a href="${base}${n}" target="_blank" rel="noopener">${n}</a>`).join('');
    },
    // —— 验证详情 ——
    selectedVal() { return state.pfSelectedVal; },
    valVerdictText() {
      const view = this.selectedVal;
      if (!view) return '';
      const verdict = view.report ? (view.report.verdict || '') : '';
      if (verdict) return '最终结论：' + (PF_VERDICT[verdict] || verdict) + (verdict === 'passed' ? '（只表示通过冻结协议，不表示未来盈利保证）' : '');
      return '验证进行中（created）：窗口执行完成后由后端按冻结规格生成最终结论。';
    },
    valVerdictCls() {
      const view = this.selectedVal;
      const verdict = view && view.report ? (view.report.verdict || '') : '';
      return verdict ? ('pf-verdict-line ' + verdict) : 'pf-verdict-line';
    },
    valMetaHtml() {
      const view = this.selectedVal;
      if (!view) return '';
      const rec = view.record || {};
      const spec = rec.spec || {};
      const report = view.report;
      const stCls = view.state === 'completed' ? 'ready' : '';
      const wr = spec.windowRule || {};
      const meta = [];
      meta.push(`<span class="st-chip ${stCls}">${PF_VAL_STATE[view.state] || view.state}</span>`);
      meta.push(`<span>证据等级 ${this.pfEvidenceChip(rec.modelEvidenceClass || (report && report.evidenceClass) || '')}</span>`);
      meta.push(`<span class="pf-num">窗口规则 训练${wr.trainDays} / 测试${wr.testDays} / 步长${wr.step} 日</span>`);
      meta.push(`<span class="pf-num">创建于 ${(rec.createdAt || '').replace('T', ' ').slice(0, 16)}</span>`);
      if (rec.supersedes) meta.push(`<span>替换 ${pfShort(rec.supersedes, 12)}</span>`);
      if (view.supersededBy && view.supersededBy.length) meta.push(`<span class="hint" style="color:#f0cd86">已被新验证替换（${view.supersededBy.length} 条），旧测试窗视为已见数据</span>`);
      return meta.join('<span style="opacity:.4"> · </span>');
    },
    valHashesText() {
      const view = this.selectedVal;
      if (!view) return '';
      const rec = view.record || {};
      const mr = (rec.spec || {}).modelRef || {};
      return `specHash（验证身份，全部字段）\n${rec.specHash || '—'}\n\n` +
        `modelRef\n${mr.modelId || '—'} · revision v${mr.revision || '—'}\nmodelHash ${mr.hash || '—'}`;
    },
    valWindowsHtml() {
      const view = this.selectedVal;
      if (!view) return '';
      const wins = view.windows || [];
      if (!wins.length) {
        return `<table><tbody><tr><td colspan="13" class="empty">${view.state === 'created' ? '尚未产生窗口结果：启动验证后逐窗写入。' : '报告未保存窗口明细。'}</td></tr></tbody></table>`;
      }
      const rows = wins.map(w => {
        const m = w.metrics || {};
        const st = w.state || '';
        const stCls = st === 'ok' ? 'ready' : st === 'error' ? 'missing' : 'stale';
        const stTxt = st === 'ok' ? '有效' : st === 'error' ? '错误' : '不足';
        const cell = (txt, cls) => `<td class="${cls || ''}">${txt}</td>`;
        let html = '<tr>' +
          cell(w.index, 'pf-num') +
          cell(`${w.testStart} ~ ${w.testEnd}`, 'pf-num') +
          cell(`<span class="st-chip ${stCls}">${stTxt}</span>`) +
          cell(m.netReturn == null ? '—' : pfPct(m.netReturn), pfCls(m.netReturn) + ' pf-num') +
          cell(m.maxDrawdown == null ? '—' : pfPct(m.maxDrawdown), 'pf-num') +
          cell(m.costDrag == null ? '—' : pfPct(m.costDrag), 'pf-num') +
          cell(m.annualTurnover == null ? '—' : pfPct(m.annualTurnover), 'pf-num') +
          cell(m.avgCashRatio == null ? '—' : pfPct(m.avgCashRatio), 'pf-num') +
          cell(m.unfilledRate == null ? '—' : pfPct(m.unfilledRate), 'pf-num') +
          cell(m.maxConcentration == null ? '—' : pfPct(m.maxConcentration), 'pf-num') +
          cell(m.informationRatio == null ? '—' : pfNum(m.informationRatio, 2), 'pf-num') +
          cell(m.baselineIncrement == null ? '—' : pfPct(m.baselineIncrement), 'pf-num') +
          cell(String(m.tradingDays || 0), 'pf-num') +
          '</tr>';
        if (w.gates && w.gates.length) {
          const pass = w.gates.filter(g => g.result === 'pass').length;
          const fail = w.gates.filter(g => g.result === 'fail').length;
          const rows2 = w.gates.map(g => `<tr><td>${esc(g.name)}</td><td>${esc(g.expected || '—')}</td><td>${esc(g.actual || '—')}</td><td class="${g.result === 'pass' ? 'pf-gate-pass' : g.result === 'fail' ? 'pf-gate-fail' : 'pf-gate-unknown'}">${PF_GATE[g.result] || g.result || '—'}</td></tr>`).join('');
          html += `<tr><td colspan="13"><details class="stat" style="margin:6px 0 2px"><summary>窗 ${w.index} 门禁（${pass} 通过 / ${fail} 未通过）</summary><table><thead><tr><th>门禁</th><th>期望</th><th>实际</th><th>结果</th></tr></thead><tbody>${rows2}</tbody></table></details></td></tr>`;
        }
        return html;
      }).join('');
      return `<table><thead><tr><th>窗</th><th>测试区间</th><th>状态</th><th>净收益</th><th>回撤</th><th>成本拖累</th><th>年换手</th><th>现金</th><th>未成交率</th><th>集中度</th><th>IR</th><th>基线增量</th><th>交易日</th></tr></thead><tbody>${rows}</tbody></table>`;
    },
    valGatesHtml() {
      const view = this.selectedVal;
      if (!view) return '';
      const gates = (view.report && view.report.gates) || [];
      if (!gates.length) {
        return `<table><tbody><tr><td colspan="5" class="empty">${view.state === 'created' ? '验证进行中：最终门禁结论由后端 Evaluate 生成。' : '报告未包含门禁明细。'}</td></tr></tbody></table>`;
      }
      const rows = gates.map(g =>
        `<tr><td>${esc(g.name)}</td><td>${esc(g.expected || '—')}</td><td>${esc(g.actual || '—')}</td><td class="${g.result === 'pass' ? 'pf-gate-pass' : g.result === 'fail' ? 'pf-gate-fail' : 'pf-gate-unknown'}">${PF_GATE[g.result] || g.result || '—'}</td><td>${esc(g.reason || '')}</td></tr>`).join('');
      const rp = view.report || {};
      return `<table><thead><tr><th>门禁</th><th>期望</th><th>实际</th><th>结果</th><th>说明</th></tr></thead><tbody>${rows}<tr><td colspan="5"><div class="hint">窗口数 ${rp.windowCount} · 有效窗口 ${rp.validWindowCount}${rp.message ? ' · ' + esc(rp.message) : ''}</div></td></tr></tbody></table>`;
    },
    valLimitations() {
      const view = this.selectedVal;
      if (!view) return '';
      const report = view.report;
      const verdict = report ? (report.verdict || '') : '';
      const lims = [];
      if (report && report.message) lims.push(report.message);
      if (verdict === 'passed') lims.push('passed 只表示通过冻结协议，不表示未来盈利保证；修改模型或门禁必须创建新验证（旧测试窗从此视为已见数据）。');
      if (!lims.length) lims.push('验证尚未完成，暂无限制声明。');
      return lims.join('；');
    },
    // —— 证据脊柱 ——
    spineNodes() {
      const m = state.pfEditModel;
      if (!m) return [];
      const factors = m.validatedFactors || [];
      const t = m.transformPipeline || {};
      const w = t.winsorize || {};
      const nz = t.neutralize || {};
      const c = m.combination || {};
      const p = m.portfolioPolicy || {};
      const lv = state.pfLastValCache[m.modelId] || {};
      const exps = this.expItems.filter(e => e.modelId === m.modelId);
      const latestExp = exps[0] || null;
      const expState = latestExp ? latestExp.status : '';
      const verdict = lv.verdict || '';
      const nodes = [
        this.pfNode('验证因子', factors.length ? 'ok' : 'empty',
          `${factors.length} 个 · ${PF_EVIDENCE[m.evidenceClass] || m.evidenceClass || '—'}`,
          factors.length ? `实现 v${factors[0].implementationVersion}` : '尚无输入证据',
          factors.some(f => f.evidenceClass === 'exploratory') ? '含探索性证据：正式验证不可能 passed' : '',
          'pfSecEvidence'),
        this.pfNode('变换', 'ok', `${t.missing || '—'} · ${t.standardize || '—'}`,
          `去极值 ${w.mode || 'none'} · 中性化 ${nz.mode || 'none'}`,
          '', 'pfSecTransform'),
        this.pfNode('合成分数', 'ok', c.method || '—',
          c.rollingIc ? `训练 ${c.rollingIc.windowYears} 年 · 收缩 ${c.rollingIc.shrinkage} · 上限 ${c.rollingIc.maxAbsWeight}` : '等权秩基线',
          '', 'pfSecCombine'),
        this.pfNode('目标权重', 'ok',
          `${p.selection === 'top_n' ? 'Top N=' + p.topN : p.selection === 'top_quantile' ? 'TopQ=' + p.topQuantile : (p.selection || '—')}`,
          `现金缓冲 ${pfPct(p.cashBuffer)}${p.minHoldings ? ' · 最少 ' + p.minHoldings + ' 只' : ''}`,
          '', 'pfSecPolicy'),
        this.pfNode('实际持仓', !latestExp ? 'empty' : (expState === 'failed' || expState === 'insufficient' ? 'degraded' : 'ok'),
          !latestExp ? '尚无运行' : (PF_RUNSTATE[expState] || expState),
          latestExp ? `研究 ${latestExp.studyRange.start || '—'} ~ ${latestExp.studyRange.end || '—'}` : '创建并启动实验后点亮',
          latestExp && (expState === 'failed' || expState === 'insufficient') ? `最近运行${PF_RUNSTATE[expState] || expState}` : '',
          'pfRunCard'),
        this.pfNode('样本外结论', !verdict ? 'empty' : (verdict === 'passed' ? 'ok' : verdict === 'failed' || verdict === 'error' ? 'error' : 'degraded'),
          !verdict ? '尚无验证' : (PF_VERDICT[verdict] || verdict),
          lv.id ? pfShort(lv.id, 12) : '创建并运行验证后点亮',
          verdict === 'failed' ? '存在未通过门禁' : verdict === 'error' ? '存在窗口执行错误' : verdict === 'insufficient' ? '有效窗口不足' : '',
          'pfValDetailCard'),
      ];
      return nodes;
    },
  },
  mounted() {
    this.loadPortfolioInit();
    watch(() => state.activeTab, n => {
      if (n === 6) this.renderPortfolioTab();
    });
    // 全局轮询状态 → 06 页按钮 busy + stale 标签
    watch(() => state.status, s => {
      if (!s) return;
      const isPfTask = s.task === 'portfolio' || s.task === 'portfolio_validation';
      if (isPfTask && s.state === 'running') state.pfTaskRunning = true;
      else if (isPfTask && (s.state === 'done' || s.state === 'error' || s.state === 'stopped')) state.pfTaskRunning = false;
      if (isPfTask) {
        if (s.state === 'running' && state.activeTab === 6) state.pfStale = true;
        else if (s.state === 'done' || s.state === 'error' || s.state === 'stopped') state.pfStale = false;
      }
    });
    // 创建实验/验证成功后刷新对应列表
    watch(() => state.pfExpDirty, () => { if (state.pfExpDirty) this.loadPfExperiments(false); });
    watch(() => state.pfValDirty, () => { if (state.pfValDirty) this.loadPfValidations(false); });
    // 表单输入 → 重置幂等 requestId（同一次保存动作网络失败重试复用）
    this.$watch('form', () => { state.pfModelRequestId = null; }, {deep: true});
  },
  unmounted() {
    dropChart(this.navC);
  },
  methods: {
    // —— 通用 ——
    pfNode(title, st, stateText, ver, degrade, target) {
      return {title, state: st, stateText, ver, degrade, target};
    },
    spineGoTo(id) {
      const el = document.getElementById(id);
      if (!el) return;
      el.scrollIntoView({block: 'start', behavior: 'auto'});
      if (el.tagName === 'DETAILS') el.open = true;
    },
    pfMetric(label, value, cls, sub) {
      const v = value == null || value === '—' ? '<span class="na">—</span>' : esc(String(value));
      return `<div class="pf-metric"><div class="pf-m-label">${esc(label)}</div><div class="pf-m-value ${cls || ''}">${v}${sub ? '<small> ' + esc(sub) + '</small>' : ''}</div></div>`;
    },
    pfH4(t) { return `<h4 style="font-family:var(--font-disp);font-size:12px;font-weight:600;letter-spacing:.06em;color:var(--gold-soft);margin:14px 0 6px">${esc(t)}</h4>`; },
    pfTable(headers, rows) {
      if (!rows || !rows.length) return '<div class="empty">报告未包含该归因数据。</div>';
      return `<div class="pf-table-wrap" style="max-height:300px"><table><thead><tr>${headers.map(h => `<th>${esc(h)}</th>`).join('')}</tr></thead><tbody>${rows}</tbody></table></div>`;
    },
    pfStateChip(verdict) {
      const cls = verdict === 'passed' ? 'ready' : verdict === 'failed' || verdict === 'error' ? 'missing' : verdict === 'insufficient' ? 'stale' : '';
      const txt = verdict ? (PF_VERDICT[verdict] || verdict) : '未验证';
      return `<span class="st-chip ${cls}">${txt}</span>`;
    },
    pfEvidenceChip(ec) {
      const txt = PF_EVIDENCE[ec] || ec || '—';
      const cls = ec === 'exploratory' ? 'stale' : ec === 'prospective' ? 'ready' : ec === 'retrospective' ? '' : '';
      return `<span class="st-chip ${cls}">${txt}</span>`;
    },
    pfEvidenceText(ec) { return PF_EVIDENCE[ec] || ec || '—'; },
    pfDir(d) { return PF_DIR[d] || d || ''; },
    pfModelState(m) {
      const c = state.pfLastValCache[m.modelId] || {verdict: ''};
      return c.verdict || 'none';
    },
    lastVal(m) { return state.pfLastValCache[m.modelId] || null; },
    factorNameOf(kind, days) {
      const e = state.factorCatalog.find(x => x.kind === kind);
      return e ? factorInstanceName(e, days) : `${kind}(${days})`;
    },
    // —— URL 恢复 / 写入（仅 06 页显示时） ——
    pfRestoreFromUrl() {
      const p = new URLSearchParams(location.search);
      const flt = (p.get('filter') || '').split('|');
      state.pfModelFilterId = flt[0] || '';
      state.pfModelFilterState = flt[1] || '';
      const s = p.get('sort') || '';
      if (s.indexOf(':') > 0) state.pfModelSort = {key: s.split(':')[0], dir: s.split(':')[1]};
      const pg = parseInt(p.get('page'), 10);
      if (Number.isInteger(pg) && pg >= 1) state.pfModelPage = pg;
      return {model: p.get('model'), revision: p.get('revision'), run: p.get('run'), val: p.get('val')};
    },
    pfUpdateUrl() {
      if (state.activeTab !== 6) return;
      const p = new URLSearchParams(location.search);
      p.set('tab', 'portfolio');
      if (state.pfEditModel && state.pfEditModel.modelId) { p.set('model', state.pfEditModel.modelId); p.set('revision', String(state.pfEditModel.revision)); }
      else { p.delete('model'); p.delete('revision'); }
      if (state.pfSelectedRun && state.pfSelectedRun.experimentId) p.set('run', state.pfSelectedRun.experimentId);
      else p.delete('run');
      if (state.pfSelectedValId) p.set('val', state.pfSelectedValId);
      else p.delete('val');
      const flt = state.pfModelFilterId + '|' + state.pfModelFilterState;
      if (flt !== '|') p.set('filter', flt); else p.delete('filter');
      p.set('sort', state.pfModelSort.key + ':' + state.pfModelSort.dir);
      p.set('page', String(state.pfModelPage));
      history.replaceState(null, '', '?' + p.toString());
    },
    // —— 模型列表 ——
    async loadPortfolioModels(silent) {
      if (state.pfModelLoading) return;
      state.pfModelLoading = true;
      if (!silent) this.modelErr = '';
      try {
        const p = new URLSearchParams({page: String(state.pfModelPage), pageSize: String(state.pfModelPageSize)});
        const d = await Api.factorModels('?' + p.toString());
        const items = d.models || d.items || [];
        const serverPageSize = d.pageSize || state.pfModelPageSize;
        if (d.total > 0 && !items.length && state.pfModelPage > Math.ceil(d.total / serverPageSize)) {
          state.pfModelPage = Math.max(1, Math.ceil(d.total / serverPageSize));
          state.pfModelLoading = false;
          this.pfUpdateUrl();
          return this.loadPortfolioModels(silent);
        }
        state.pfModelPageData = d;
        state.pfModels = items;
        if (!state.pfEvidenceLoaded) {
          try {
            const full = await Api.factorModels('');
            state.pfEvidenceModels = full.models || [];
            state.pfEvidenceLoaded = true;
          } catch (e) { /* 证据聚合失败：下次加载重试 */ }
        }
        state.pfStale = false;
        this.aggregateValidatedFactors();
        await Promise.all(state.pfModels.map(m => this.ensureLastVal(m.modelId)));
      } catch (e) {
        this.modelErr = '模型列表加载失败：' + pfErrMsg(e);
      } finally {
        state.pfModelLoading = false;
      }
    },
    async ensureLastVal(modelId) {
      if (state.pfLastValCache[modelId] && state.pfLastValCache[modelId]._ok) return;
      try {
        const d = await Api.portfolioValidations(`?modelId=${encodeURIComponent(modelId)}&page=1&pageSize=1`);
        const items = (d.validations && d.validations.items) || [];
        const it = items[0];
        state.pfLastValCache[modelId] = it
          ? {verdict: it.verdict || '', id: it.id, createdAt: it.createdAt || '', _ok: true}
          : {verdict: '', id: '', createdAt: '', _ok: true};
      } catch (e) {
        state.pfLastValCache[modelId] = {verdict: '', id: '', createdAt: '', _ok: false};
      }
    },
    aggregateValidatedFactors() {
      const map = new Map();
      (state.pfEvidenceModels.length ? state.pfEvidenceModels : state.pfModels).forEach(m => (m.validatedFactors || []).forEach(f => {
        if (!f.validationId || map.has(f.validationId)) return;
        map.set(f.validationId, {validationId: f.validationId, factorKind: f.factorKind, factorDays: f.factorDays,
          implementationVersion: f.implementationVersion, direction: f.direction, evidenceClass: f.evidenceClass,
          primaryHorizon: f.primaryHorizon, candidateRevision: f.candidateRevision, candidateId: f.candidateId,
          sourceModel: m.modelId, sourceRevision: m.revision, createdAt: m.createdAt});
      }));
      state.pfValidatedFactors = [...map.values()].sort((a, b) => a.factorKind.localeCompare(b.factorKind) || a.factorDays - b.factorDays);
    },
    filteredSortedModels() {
      let list = state.pfModels.slice();
      if (state.pfModelFilterId) {
        const q = state.pfModelFilterId.toLowerCase();
        list = list.filter(m => m.modelId.toLowerCase().includes(q));
      }
      if (state.pfModelFilterState) {
        list = list.filter(m => (this.pfModelState(m) === state.pfModelFilterState) || (state.pfModelFilterState === 'none' && this.pfModelState(m) === 'none'));
      }
      const k = state.pfModelSort.key, dir = state.pfModelSort.dir === 'asc' ? 1 : -1;
      const rank = {none: 0, passed: 1, failed: 2, insufficient: 3, error: 4};
      const ecRank = {exploratory: 0, retrospective: 1, prospective: 2};
      const cmp = (a, b) => {
        let av, bv;
        if (k === 'revision') { av = a.revision; bv = b.revision; }
        else if (k === 'evidenceClass') { av = ecRank[a.evidenceClass] != null ? ecRank[a.evidenceClass] : -1; bv = ecRank[b.evidenceClass] != null ? ecRank[b.evidenceClass] : -1; }
        else if (k === 'factorCount') { av = (a.validatedFactors || []).length; bv = (b.validatedFactors || []).length; }
        else if (k === 'state') { av = rank[this.pfModelState(a)] != null ? rank[this.pfModelState(a)] : -1; bv = rank[this.pfModelState(b)] != null ? rank[this.pfModelState(b)] : -1; }
        else if (k === 'lastValidation') { av = (this.lastVal(a) && this.lastVal(a).createdAt) || ''; bv = (this.lastVal(b) && this.lastVal(b).createdAt) || ''; }
        else { av = a.createdAt || ''; bv = b.createdAt || ''; }
        if (av === bv) return a.modelId < b.modelId ? -1 : 1;
        return av > bv ? dir : -dir;
      };
      list.sort(cmp);
      return list;
    },
    sortAria(key) {
      if (state.pfModelSort.key !== key) return 'none';
      return state.pfModelSort.dir === 'asc' ? 'ascending' : 'descending';
    },
    pfModelPageMove(d) {
      state.pfModelPage = Math.max(1, state.pfModelPage + d);
      this.loadPortfolioModels(false);
      this.pfUpdateUrl();
    },
    pfSortModels(key) {
      if (state.pfModelSort.key === key) state.pfModelSort.dir = state.pfModelSort.dir === 'asc' ? 'desc' : 'asc';
      else { state.pfModelSort.key = key; state.pfModelSort.dir = 'asc'; }
      this.pfUpdateUrl();
    },
    pfModelFilterDebounced() {
      clearTimeout(this._mfT);
      this._mfT = setTimeout(() => {
        state.pfModelPage = 1;
        this.loadPortfolioModels(false);
        this.pfUpdateUrl();
      }, 250);
    },
    pfModelFilterStateChange() {
      state.pfModelPage = 1;
      this.loadPortfolioModels(false);
      this.pfUpdateUrl();
    },
    // —— 模型编辑 / 创建 ——
    fillPfForm(m) {
      const t = m.transformPipeline || {};
      const w = t.winsorize || {};
      const n = t.neutralize || {};
      const c = m.combination || {};
      const p = m.portfolioPolicy || {};
      const e = m.execution || {};
      const cost = e.cost || {};
      const f = this.form;
      f.createdBy = m.createdBy || '';
      f.researchQuestion = m.researchQuestion || '';
      f.hypothesis = m.hypothesis || '';
      f.missing = t.missing || 'exclude';
      f.winsorize = w.mode || 'none';
      f.winsorizeParam = w.mode === 'quantile' ? (w.quantile || '') : w.mode === 'mad' ? (w.madK || '') : '';
      f.neutralize = n.mode || 'none';
      f.industryDataset = n.industryDataset || '';
      f.sizeDataset = n.sizeDataset || '';
      f.standardize = t.standardize || 'rank';
      f.combineMethod = c.method || 'equal_weight_rank';
      const ri = c.rollingIc;
      f.icWindow = ri ? ri.windowYears : 3;
      f.icShrink = ri ? ri.shrinkage : 0.5;
      f.icMax = ri ? ri.maxAbsWeight : 0.5;
      f.icFallback = ri ? ri.fallback : 'equal_weight';
      f.selection = p.selection || 'top_n';
      f.topN = p.topN || 20;
      f.topQuantile = p.topQuantile || 0.2;
      f.maxStockWeight = p.maxStockWeight || 0;
      f.maxIndustryWeight = p.maxIndustryWeight || 0;
      f.maxTurnover = p.maxTurnover || 0;
      f.minHoldings = p.minHoldings || 0;
      f.cashBuffer = p.cashBuffer || 0;
      f.rebalance = e.rebalance || 'daily';
      f.fillAt = e.fillAt || 'next_open';
      f.sellFirst = e.sellFirst !== false;
      f.t1 = e.t1Restriction !== false;
      f.carryUnfilled = !!e.carryUnfilled;
      f.lotSize = e.lotSize || 100;
      f.commission = cost.commissionRate || 0;
      f.stampDuty = cost.stampDutyRate || 0;
      f.transferFee = cost.transferFeeRate || 0;
      f.slippage = cost.slippage || 0;
      f.minCommission = cost.minCommission || 0;
      state.pfSelectedValidationIds = (m.validatedFactors || []).map(x => x.validationId).filter(Boolean);
    },
    pfDefaultForm() {
      const f = this.form;
      f.createdBy = ''; f.researchQuestion = ''; f.hypothesis = '';
      f.missing = 'exclude'; f.winsorize = 'none'; f.winsorizeParam = ''; f.neutralize = 'none';
      f.industryDataset = ''; f.sizeDataset = ''; f.standardize = 'rank';
      f.combineMethod = 'equal_weight_rank'; f.icWindow = 3; f.icShrink = 0.5; f.icMax = 0.5; f.icFallback = 'equal_weight';
      f.selection = 'top_n'; f.topN = 20; f.topQuantile = 0.2;
      f.maxStockWeight = 0; f.maxIndustryWeight = 0; f.maxTurnover = 0; f.minHoldings = 5; f.cashBuffer = 0;
      f.rebalance = 'daily'; f.fillAt = 'next_open'; f.sellFirst = true; f.t1 = true; f.carryUnfilled = false;
      f.lotSize = 100; f.commission = 0.0003; f.stampDuty = 0.001; f.transferFee = 0; f.slippage = 0.01; f.minCommission = 5;
      state.pfSelectedValidationIds = [];
    },
    openPfCreate() {
      state.pfEditModel = null;
      state.pfSelectedModelId = '';
      this.pfDefaultForm();
      this.editOpen = true;
      this.editStaleText = '';
      this.editMsg = {text: '', ok: true};
      state.pfModelRequestId = null;
      this.$nextTick(() => {
        const el = document.getElementById('pfEditCard');
        if (el) el.scrollIntoView({block: 'start'});
        const cb = document.getElementById('pfCreatedBy');
        if (cb) cb.focus();
      });
      this.pfUpdateUrl();
    },
    closePfEdit() {
      this.editOpen = false;
      this.$nextTick(() => {
        const el = document.getElementById('btnPfCreateModel');
        if (el) el.focus();
      });
    },
    async selectPfModel(modelId, revision) {
      const seq = ++state.pfSeq;
      state.pfSelectedModelId = modelId;
      try {
        let url = modelId;
        if (revision) url += '?revision=' + revision;
        const d = await Api.factorModel(url);
        if (seq !== state.pfSeq) return;
        const m = d.model;
        state.pfEditModel = m;
        this.fillPfForm(m);
        this.editOpen = true;
        this.editStaleText = '';
        const latest = state.pfModels.find(x => x.modelId === modelId);
        if (latest && latest.revision > m.revision) {
          this.editStaleText = `该模型已有更新 revision v${latest.revision}（当前查看 v${m.revision}）。保存将基于 v${m.revision} 追加新 revision。`;
        }
        this.editMsg = {text: '', ok: true};
        this.pfUpdateUrl();
        this.$nextTick(() => {
          const el = document.getElementById('pfEditCard');
          if (el) el.scrollIntoView({block: 'start'});
          const t = document.getElementById('pfEditTitle');
          if (t) t.focus();
        });
      } catch (e) {
        if (seq !== state.pfSeq) return;
        this.editMsg = {text: '模型加载失败：' + pfErrMsg(e), ok: false};
      }
    },
    buildModelRequest() {
      const f = this.form;
      const wMode = f.winsorize;
      const winsorize = {mode: wMode};
      if (wMode === 'quantile') winsorize.quantile = parseFloat(f.winsorizeParam) || 0;
      if (wMode === 'mad') winsorize.madK = parseFloat(f.winsorizeParam) || 0;
      const nMode = f.neutralize;
      const neutralize = {mode: nMode};
      if (nMode === 'industry_size') {
        neutralize.industryDataset = f.industryDataset.trim();
        neutralize.sizeDataset = f.sizeDataset.trim();
      }
      const combination = {method: f.combineMethod};
      if (combination.method === 'rolling_ic_weight') {
        combination.rollingIc = {
          windowYears: parseInt(f.icWindow, 10) || 3,
          shrinkage: parseFloat(f.icShrink) || 0,
          maxAbsWeight: parseFloat(f.icMax) || 0,
          fallback: f.icFallback,
        };
      }
      const selection = f.selection;
      const policy = {
        selection,
        maxStockWeight: parseFloat(f.maxStockWeight) || 0,
        maxIndustryWeight: parseFloat(f.maxIndustryWeight) || 0,
        maxTurnover: parseFloat(f.maxTurnover) || 0,
        minHoldings: parseInt(f.minHoldings, 10) || 0,
        cashBuffer: parseFloat(f.cashBuffer) || 0,
      };
      if (selection === 'top_n') policy.topN = parseInt(f.topN, 10) || 20;
      else policy.topQuantile = parseFloat(f.topQuantile) || 0.2;
      const req = {
        requestId: state.pfModelRequestId,
        createdBy: f.createdBy.trim(),
        researchQuestion: f.researchQuestion.trim(),
        hypothesis: f.hypothesis.trim(),
        factorValidations: state.pfSelectedValidationIds.slice(),
        transformPipeline: {
          missing: f.missing,
          winsorize, neutralize,
          standardize: f.standardize,
        },
        combination,
        portfolioPolicy: policy,
        execution: {
          rebalance: f.rebalance,
          fillAt: f.fillAt,
          sellFirst: f.sellFirst,
          t1Restriction: f.t1,
          carryUnfilled: f.carryUnfilled,
          lotSize: parseInt(f.lotSize, 10) || 100,
          cost: {
            commissionRate: parseFloat(f.commission) || 0,
            stampDutyRate: parseFloat(f.stampDuty) || 0,
            transferFeeRate: parseFloat(f.transferFee) || 0,
            slippage: parseFloat(f.slippage) || 0,
            minCommission: parseFloat(f.minCommission) || 0,
          },
        },
        benchmark: {},
      };
      if (state.pfEditModel && state.pfEditModel.modelId) {
        req.modelId = state.pfEditModel.modelId;
        req.baseRevision = state.pfEditModel.revision;
      }
      return req;
    },
    validatePfModel() {
      const f = this.form;
      if (!f.createdBy.trim()) { this.editMsg = {text: '请填写研究者（createdBy）', ok: false}; return false; }
      if (!f.researchQuestion.trim()) { this.editMsg = {text: '请填写研究问题', ok: false}; return false; }
      if (!f.hypothesis.trim()) { this.editMsg = {text: '请填写假设', ok: false}; return false; }
      if (!state.pfSelectedValidationIds.length) { this.editMsg = {text: '请至少勾选一个已验证因子作为输入证据', ok: false}; return false; }
      if (f.neutralize === 'industry_size' && (!f.industryDataset.trim() || !f.sizeDataset.trim())) {
        this.editMsg = {text: '中性化 mode=industry_size 必须同时声明行业与市值数据集', ok: false}; return false;
      }
      if (f.combineMethod === 'rolling_ic_weight') {
        const w = parseInt(f.icWindow, 10);
        if (!(w >= 1 && w <= 10)) { this.editMsg = {text: '滚动 IC 训练窗应为 1-10 年', ok: false}; return false; }
        const sh = parseFloat(f.icShrink);
        if (!(sh >= 0 && sh <= 1)) { this.editMsg = {text: '收缩系数应为 [0,1]', ok: false}; return false; }
        const mx = parseFloat(f.icMax);
        if (!(mx > 0 && mx <= 1)) { this.editMsg = {text: '单因子权重上限应为 (0,1]', ok: false}; return false; }
      }
      return true;
    },
    async savePfModel() {
      this.editMsg = {text: '', ok: true};
      if (!this.validatePfModel()) return;
      if (!state.pfModelRequestId) state.pfModelRequestId = crypto.randomUUID();
      const body = this.buildModelRequest();
      try {
        const d = await Api.saveFactorModel(body);
        state.pfModelRequestId = null;
        const m = d.model;
        this.editMsg = {text: d.created
          ? (state.pfEditModel && state.pfEditModel.modelId
            ? `已保存为新 revision v${m.revision}（模型 ${m.modelId}）`
            : `已创建模型 ${m.modelId}（revision v1）`)
          : '幂等命中：该请求此前已保存。', ok: true};
        state.pfModelPage = 1;
        state.pfEvidenceLoaded = false; // 模型集合已变：全量证据下次加载时重拉
        await this.loadPortfolioModels(true);
        await this.ensureLastVal(m.modelId);
        await this.selectPfModel(m.modelId, m.revision);
      } catch (e) {
        // 服务端错误保留用户输入与 requestId（重试不重复创建）
        this.editMsg = {text: pfErrMsg(e), ok: false};
      }
    },
    onEvidenceToggle(f, e) {
      const ids = state.pfSelectedValidationIds;
      if (e.target.checked) {
        if (!ids.includes(f.validationId)) ids.push(f.validationId);
      } else {
        state.pfSelectedValidationIds = ids.filter(x => x !== f.validationId);
      }
      state.pfModelRequestId = null;
    },
    // —— 创建实验 / 验证（对话框由根组件挂载） ——
    openPfExperimentDlg(m) {
      const model = m || state.pfEditModel;
      if (!model || !model.modelId) { this.editMsg = {text: '请先选择或保存一个模型，再创建实验。', ok: false}; return; }
      this.openPfExpDlg(model);
    },
    openPfValidationDlg(m) {
      const model = m || state.pfEditModel;
      if (!model || !model.modelId) { this.editMsg = {text: '请先选择或保存一个模型，再创建验证。', ok: false}; return; }
      this.openPfValDlg(model);
    },
    // 供创建验证对话框读取“⑥ 门禁”当前值（含窗口天数）
    getPfGates() {
      const f = this.form;
      return {
        _trainDays: f.trainDays, _testDays: f.testDays, _step: f.step,
        minValidWindows: parseFloat(f.minValidWindows) || 0,
        minTradingDays: parseFloat(f.minTradingDays) || 0,
        minNetReturn: parseFloat(f.minNetReturn) || 0,
        maxDrawdown: parseFloat(f.maxDrawdown) || 0,
        maxCostDrag: parseFloat(f.maxCostDrag) || 0,
        minIR: parseFloat(f.minIR) || 0,
        minExcessStability: parseFloat(f.minExcessStability) || 0,
        maxTurnoverGate: parseFloat(f.maxTurnoverGate) || 0,
        maxCashResidual: parseFloat(f.maxCashResidual) || 0,
        maxUnfilled: parseFloat(f.maxUnfilled) || 0,
        maxConcentration: parseFloat(f.maxConcentration) || 0,
        minDirectionConsistency: parseFloat(f.minDirectionConsistency) || 0,
        minBaselineIncrement: parseFloat(f.minBaselineIncrement) || 0,
        maxDegradedWindows: parseInt(f.maxDegradedWindows, 10) || 0,
      };
    },
    // —— 运行列表 ——
    async loadPfExperiments(silent) {
      try {
        const p = new URLSearchParams({page: String(state.pfExpPage), pageSize: String(state.pfExpPageSize)});
        if (state.pfExpFilterModel) p.set('modelId', state.pfExpFilterModel);
        if (state.pfExpFilterStatus) p.set('status', state.pfExpFilterStatus);
        const d = await Api.portfolioExperiments('?' + p.toString());
        state.pfExperiments = d.experiments;
        if (!silent) this.expErr = '';
      } catch (e) {
        this.expErr = '运行列表加载失败：' + pfErrMsg(e);
      }
    },
    pfExpPageMove(d) {
      state.pfExpPage = Math.max(1, (state.pfExperiments ? state.pfExperiments.page : state.pfExpPage) + d);
      this.loadPfExperiments(false);
    },
    pfExpFilterDebounced() {
      clearTimeout(this._expT);
      this._expT = setTimeout(() => {
        state.pfExpPage = 1;
        this.loadPfExperiments(false);
      }, 250);
    },
    pfExpFilterStatusChange() { state.pfExpPage = 1; this.loadPfExperiments(false); },
    // —— 验证列表 ——
    async loadPfValidations(silent) {
      try {
        const p = new URLSearchParams({page: String(state.pfValPage), pageSize: String(state.pfValPageSize)});
        if (state.pfValFilterModel) p.set('modelId', state.pfValFilterModel);
        if (state.pfValFilterVerdict) p.set('verdict', state.pfValFilterVerdict);
        const d = await Api.portfolioValidations('?' + p.toString());
        state.pfValidations = d.validations;
        if (!silent) this.valErr = '';
      } catch (e) {
        this.valErr = '验证列表加载失败：' + pfErrMsg(e);
      }
    },
    pfValPageMove(d) {
      state.pfValPage = Math.max(1, (state.pfValidations ? state.pfValidations.page : state.pfValPage) + d);
      this.loadPfValidations(false);
    },
    pfValFilterDebounced() {
      clearTimeout(this._valT);
      this._valT = setTimeout(() => {
        state.pfValPage = 1;
        this.loadPfValidations(false);
      }, 250);
    },
    pfValFilterVerdictChange() { state.pfValPage = 1; this.loadPfValidations(false); },
    // —— 运行 / 停止 ——
    async startPfRun(id) {
      try {
        const d = await Api.startPortfolioRun(id);
        this.startTask(d.task, {done: -1, total: -1, progress: -1, phase: 'queued', runId: id});
        state.pfTaskRunning = true;
        if (state.pfSelectedRun && state.pfSelectedRun.experimentId === id) await this.openPfRun(id);
        else await this.loadPfExperiments(false);
      } catch (e) {
        this.runErr = '启动运行失败：' + pfErrMsg(e);
      }
    },
    async startPfValidation(id) {
      try {
        const d = await Api.startPortfolioRun(id);
        this.startTask(d.task, {done: -1, total: -1, progress: -1, phase: 'queued', runId: id});
        state.pfTaskRunning = true;
        if (state.pfSelectedValId === id) await this.openPfValDetail(id);
        else await this.loadPfValidations(false);
      } catch (e) {
        this.valDetailErr = '启动验证失败：' + pfErrMsg(e);
      }
    },
    async pfStopRun(id) {
      try {
        await Api.stopPortfolioRun(id);
      } catch (e) {
        this.runErr = '停止失败：' + pfErrMsg(e);
      }
    },
    // —— 运行详情 ——
    async openPfRun(id) {
      const seq = ++state.pfSeq;
      state.pfSelectedRun = null;
      state.pfSelectedRunReport = null;
      this.runCardOpen = true;
      this.runErr = '';
      this.runLoadingText = '加载运行详情中…';
      this.pfUpdateUrl();
      try {
        const d = await Api.experiment(id);
        if (seq !== state.pfSeq) return;
        const exp = d.experiment;
        state.pfSelectedRun = exp;
        let rep = null;
        if (exp.status === 'completed' && exp.manifest) {
          try {
            rep = await Api.experimentArtifact(id, 'report.json');
          } catch (e) { /* 报告读取失败时仍展示状态与产物入口 */ }
        }
        if (seq !== state.pfSeq) return;
        state.pfSelectedRunReport = rep;
        this.pfUpdateUrl();
        if (rep) {
          this.runLoadingText = '';
          this.$nextTick(() => this.renderNav(rep));
        } else {
          const st = exp.status || '';
          this.runLoadingText = (st === 'queued' || st === 'running')
            ? (st === 'running' ? '运行中：完成后在此展示报告（进度见右下角任务卡）。' : '实验已创建（queued）：点击“启动运行”后开始执行。')
            : (st === 'completed' ? '运行已完成但报告读取失败，可下载产物核对。' : (exp.error || '运行未产出报告（失败/取消/证据不足不发布正式报告）。'));
        }
        this.$nextTick(() => {
          const card = document.getElementById('pfRunCard');
          if (card) card.scrollIntoView({block: 'start'});
          const t = document.getElementById('pfRunTitle');
          if (t) t.focus();
        });
      } catch (e) {
        if (seq !== state.pfSeq) return;
        state.pfSelectedRun = null;
        this.runErr = '运行详情加载失败：' + pfErrMsg(e);
      }
    },
    renderNav(rep) {
      const el = this.$refs.navChart;
      if (!el) return;
      const nav = rep.nav || [];
      if (!nav.length) {
        if (this.navC) { dropChart(this.navC); this.navC = null; }
        return;
      }
      if (!this.navC) { this.navC = mountChart(el); if (!this.navC) return; }
      this.navC.setOption({
        backgroundColor: 'transparent',
        title: {text: '净值（毛 / 净）', left: 10, top: 6, textStyle: {fontSize: 13, color: '#e9eef8'}},
        tooltip: {trigger: 'axis'},
        legend: {top: 6, right: 10, textStyle: {color: '#c2ccdd', fontSize: 11}},
        grid: {left: 60, right: 20, top: 48, bottom: 56},
        xAxis: {type: 'category', data: nav.map(x => x.date), axisLabel: {fontSize: 10}},
        yAxis: {type: 'value', scale: true},
        series: [
          {name: '毛净值', type: 'line', data: nav.map(x => x.grossNav), symbol: 'none', lineStyle: {width: 1.5, color: NAV_COLORS.gross}},
          {name: '净净值', type: 'line', data: nav.map(x => x.netNav), symbol: 'none', lineStyle: {width: 1.5, color: NAV_COLORS.net}},
        ],
      }, true);
    },
    // —— 验证详情 ——
    async openPfValDetail(id) {
      const seq = ++state.pfSeq;
      state.pfSelectedVal = null;
      state.pfSelectedValId = id;
      this.valCardOpen = true;
      this.valDetailErr = '';
      this.pfUpdateUrl();
      try {
        const d = await Api.validation(id);
        if (seq !== state.pfSeq) return;
        state.pfSelectedVal = d.validation;
        this.$nextTick(() => {
          const card = document.getElementById('pfValDetailCard');
          if (card) card.scrollIntoView({block: 'start'});
          const t = document.getElementById('pfValDetailTitle');
          if (t) t.focus();
        });
      } catch (e) {
        if (seq !== state.pfSeq) return;
        this.valDetailErr = '验证详情加载失败：' + pfErrMsg(e);
      }
    },
    valRecId() { return this.selectedVal && this.selectedVal.record ? this.selectedVal.record.id : ''; },
    // —— 任务完成钩子（根组件轮询 done 分支调用） ——
    async pfOnTaskDone(runId, task) {
      state.pfTaskRunning = false;
      state.pfStale = false;
      if (task === 'portfolio') {
        await this.loadPfExperiments(true);
        if (state.pfSelectedRun && state.pfSelectedRun.experimentId === runId) await this.openPfRun(runId);
      } else {
        await this.loadPfValidations(true);
        if (state.pfSelectedValId === runId) await this.openPfValDetail(runId);
      }
      await this.loadPortfolioModels(true); // 模型“最近验证”状态可能变化
    },
    // —— 06 页入口 ——
    async renderPortfolioTab() {
      if (!this.pfInitialized) return;
      const q = this.pfRestoreFromUrl();
      await Promise.all([this.loadPortfolioModels(false), this.loadPfExperiments(false), this.loadPfValidations(false)]);
      if (q.model) await this.selectPfModel(q.model, q.revision ? parseInt(q.revision, 10) : null);
      if (q.run) await this.openPfRun(q.run);
      if (q.val) await this.openPfValDetail(q.val);
      if (!q.model && !q.run && !q.val) { /* 脊柱随模型选择渲染 */ }
    },
    async loadPortfolioInit() {
      this.pfInitialized = true;
      await this.loadPortfolioModels(false);
      await this.loadPfExperiments(false);
      await this.loadPfValidations(false);
      try {
        const d = await Api.candidates(false);
        state.pfCandidates = d.candidates || [];
      } catch (e) { /* 候选参照失败不影响主流程 */ }
    },
  },
  template: `
<div>
  <!-- 证据脊柱 -->
  <div class="card">
    <h3>证据脊柱</h3>
    <div class="hint" style="margin:-10px 0 2px">把已冻结的验证证据按真实数据流连接到样本外结论；点击节点定位到对应配置或报告。节点只表达审计关系，不代替详细表格，也不展示脱离证据的“魔法总分”。</div>
    <div class="pf-spine" v-if="spineNodes.length">
      <template v-for="(n, i) in spineNodes" :key="i">
        <button type="button" class="pf-spine-node" :data-node-state="n.state" @click="spineGoTo(n.target)">
          <div class="pf-node-title">{{ n.title }}</div>
          <div class="pf-node-state"><span class="pf-node-dot" aria-hidden="true"></span><span>{{ n.stateText }}</span></div>
          <div v-if="n.ver" class="pf-node-ver">{{ n.ver }}</div>
          <div v-if="n.degrade" class="pf-node-degrade">⚠ {{ n.degrade }}</div>
        </button>
        <span v-if="i < spineNodes.length - 1" class="pf-spine-arrow" aria-hidden="true">→</span>
      </template>
    </div>
    <div class="empty" v-else style="margin-top:12px" role="status">尚未选择模型：从下方模型列表选择（或创建）模型后，证据脊柱将按该模型的数据流点亮。</div>
  </div>

  <!-- 模型列表 -->
  <div class="card" style="margin-top:18px">
    <h3>模型列表 <span class="hint">revision 为追加式不可变快照；编辑即产生新 revision，不原地覆盖。</span><span class="pf-stale-tag" v-if="state.pfStale && state.activeTab===6">数据可能已过期</span></h3>
    <div class="pf-toolbar">
      <button type="button" class="secondary" @click="loadPortfolioModels(false)" :disabled="state.pfTaskRunning">刷新</button>
      <button type="button" class="primary" id="btnPfCreateModel" @click="openPfCreate" :disabled="state.pfTaskRunning">创建模型</button>
      <span class="pf-filters">
        <label for="pfModelFilterId">模型 ID</label>
        <input type="text" id="pfModelFilterId" v-model.trim="state.pfModelFilterId" @input="pfModelFilterDebounced" placeholder="fm_…" aria-label="按模型 ID 筛选">
        <label for="pfModelFilterState">状态</label>
        <select id="pfModelFilterState" v-model="state.pfModelFilterState" @change="pfModelFilterStateChange" aria-label="按最近验证结论筛选">
          <option value="">全部状态</option>
          <option value="passed">已验证通过</option>
          <option value="failed">验证未通过</option>
          <option value="insufficient">证据不足</option>
          <option value="error">验证执行错误</option>
          <option value="none">未验证</option>
        </select>
      </span>
    </div>
    <template v-if="modelRows.length">
      <div class="pf-table-wrap">
        <table id="pfModelTable">
          <thead><tr>
            <th class="pf-idcol">模型</th>
            <th><button type="button" class="pf-sortbtn" :aria-sort="sortAria('state')" @click="pfSortModels('state')">状态</button></th>
            <th><button type="button" class="pf-sortbtn" :aria-sort="sortAria('revision')" @click="pfSortModels('revision')">revision</button></th>
            <th><button type="button" class="pf-sortbtn" :aria-sort="sortAria('evidenceClass')" @click="pfSortModels('evidenceClass')">证据等级</button></th>
            <th><button type="button" class="pf-sortbtn" :aria-sort="sortAria('factorCount')" @click="pfSortModels('factorCount')">因子数</button></th>
            <th><button type="button" class="pf-sortbtn" :aria-sort="sortAria('lastValidation')" @click="pfSortModels('lastValidation')">最近验证</button></th>
            <th>操作</th>
          </tr></thead>
          <tbody id="pfModelBody">
            <tr v-for="m in modelRows" :key="m.modelId" :data-model="m.modelId">
              <td class="pf-idcol">
                <button type="button" class="pf-sortbtn" style="border:0;padding:0;margin:0;font-family:var(--font-num);font-size:12px;color:#9cc2ff;font-weight:600" :aria-label="'查看模型 ' + m.modelId" @click="selectPfModel(m.modelId, null)">{{ m.modelId }}</button>
                <div class="compat-note">{{ (m.createdAt || '').replace('T', ' ').slice(0, 16) }} · {{ m.createdBy || '' }}</div>
              </td>
              <td v-html="pfStateChip(pfModelState(m))"></td>
              <td><span class="pf-num">v{{ m.revision }}</span></td>
              <td v-html="pfEvidenceChip(m.evidenceClass)"></td>
              <td><span class="pf-num">{{ (m.validatedFactors || []).length }}</span></td>
              <td>
                <template v-if="lastVal(m)">
                  <span class="pf-num">{{ pfShort(lastVal(m).id, 10) }}</span>
                  <div class="compat-note">{{ (lastVal(m).createdAt || '').replace('T', ' ').slice(0, 16) }}</div>
                </template>
                <span v-else class="compat-note">—</span>
              </td>
              <td class="pf-ops" style="white-space:nowrap">
                <button type="button" class="secondary" style="min-height:26px;padding:3px 11px;font-size:11px;margin-right:6px" @click="selectPfModel(m.modelId, null)">编辑</button>
                <button type="button" class="secondary" style="min-height:26px;padding:3px 11px;font-size:11px;margin-right:6px" @click="openPfExperimentDlg(m)">实验</button>
                <button type="button" class="secondary" style="min-height:26px;padding:3px 11px;font-size:11px;margin-right:6px" @click="openPfValidationDlg(m)">验证</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>
    <div class="empty" v-else-if="modelEmptyText" id="pfModelEmpty" role="status">{{ modelEmptyText }}</div>
    <div class="warnbar" v-if="modelErr" role="alert">{{ modelErr }}</div>
    <div class="pf-pager" v-if="modelPages > 1">
      <button type="button" @click="pfModelPageMove(-1)" :disabled="state.pfModelPage <= 1">上一页</button>
      <span>{{ modelPageInfo }}</span>
      <button type="button" @click="pfModelPageMove(1)" :disabled="state.pfModelPage >= modelPages">下一页</button>
    </div>
    <div class="hint">{{ modelHint }}</div>
  </div>

  <!-- 模型编辑 / 创建 -->
  <div class="card" id="pfEditCard" v-show="editOpen" style="margin-top:18px">
    <h3 id="pfEditTitle" tabindex="-1">{{ editTitle }}</h3>
    <div class="warnbar" v-if="editStaleText" role="status">{{ editStaleText }}</div>
    <div class="pf-form-grid">
      <div class="pf-field"><label for="pfCreatedBy">研究者</label><input type="text" id="pfCreatedBy" v-model="form.createdBy" maxlength="80" autocomplete="off"></div>
      <div class="pf-field full"><label for="pfResearchQuestion">研究问题</label><input type="text" id="pfResearchQuestion" v-model="form.researchQuestion" maxlength="200" autocomplete="off"></div>
      <div class="pf-field full"><label for="pfHypothesis">假设</label><textarea id="pfHypothesis" class="resize-none" v-model="form.hypothesis" maxlength="2000" style="height:64px" placeholder="记录待验证假设；保存后进入模型 hash 之外的研究记录"></textarea></div>
    </div>

    <details class="pf-section" id="pfSecEvidence" open>
      <summary>① 输入证据（已验证因子）</summary>
      <div class="hint">模型只能引用冻结的验证因子；勾选后保存产生模型（新）revision。可选项聚合自已有模型的已验证因子；候选库中的因子须先完成 v1 验证后才能进入组合模型。</div>
      <div class="pf-evidence-list">
        <div class="pf-evidence-item" v-for="f in state.pfValidatedFactors" :key="f.validationId">
          <input type="checkbox" :id="'pfev_' + f.validationId" :checked="state.pfSelectedValidationIds.includes(f.validationId)" @change="onEvidenceToggle(f, $event)">
          <div class="pf-ev-main">
            <div class="pf-ev-name">{{ factorNameOf(f.factorKind, f.factorDays) }}</div>
            <div class="pf-ev-meta"><b>验证</b> {{ pfShort(f.validationId, 16) }} · <b>方向</b> {{ pfDir(f.direction) }} · <b>候选</b> v{{ f.candidateRevision }} · <b>实现</b> v{{ f.implementationVersion }} · <b>主预测周期</b> {{ f.primaryHorizon }} 交易日 · <b>来源</b> {{ pfShort(f.sourceModel, 10) }} v{{ f.sourceRevision }}</div>
          </div>
          <span class="pf-ev-ec" :class="f.evidenceClass || ''">{{ pfEvidenceText(f.evidenceClass) }}</span>
        </div>
      </div>
      <div class="empty" v-if="!state.pfValidatedFactors.length" style="margin-top:10px" role="status">暂无可用验证因子：请先在“05 候选因子”完成候选并做 v1 验证（Task 1-9 生成验证记录），或从已有模型中选择因子。</div>
      <div class="pf-cand-ref" id="pfEvidenceRef">候选库参照：{{ state.pfCandidates.length }} 个候选因子（未进入组合模型）。候选须先完成 v1 验证并冻结后才能作为组合输入证据；这里仅聚合已有模型中的已验证因子。</div>
    </details>

    <details class="pf-section" id="pfSecTransform">
      <summary>② 变换（截面变换流水线，顺序固定）</summary>
      <div class="pf-form-grid">
        <div class="pf-field"><label for="pfMissing">缺失策略</label>
          <select id="pfMissing" v-model="form.missing">
            <option value="exclude">exclude（缺失不入截面）</option>
            <option value="cross_section_median">cross_section_median（当日中位数填充）</option>
            <option value="renormalize_available">renormalize_available（按可用因子重归一化）</option>
          </select></div>
        <div class="pf-field"><label for="pfWinsorize">去极值</label>
          <select id="pfWinsorize" v-model="form.winsorize">
            <option value="none">none（不去极值）</option>
            <option value="quantile">quantile（分位截尾）</option>
            <option value="mad">mad（MAD 截尾）</option>
          </select></div>
        <div class="pf-field" v-show="winsorizeRowVisible"><label>{{ winsorizeLabel }}</label>
          <input type="number" id="pfWinsorizeParam" v-model="form.winsorizeParam" step="any"></div>
        <div class="pf-field"><label for="pfNeutralize">风险中性化</label>
          <select id="pfNeutralize" v-model="form.neutralize">
            <option value="none">none</option>
            <option value="industry_size">industry_size（行业+市值截面回归）</option>
          </select></div>
        <div class="pf-field" v-show="neutralizeRowVisible"><label for="pfIndustryDataset">行业数据集</label><input type="text" id="pfIndustryDataset" v-model="form.industryDataset" placeholder="如 industry/申万一级"></div>
        <div class="pf-field" v-show="neutralizeRowVisible"><label for="pfSizeDataset">市值数据集</label><input type="text" id="pfSizeDataset" v-model="form.sizeDataset" placeholder="如 size/ln_float_cap"></div>
        <div class="pf-field"><label for="pfStandardize">截面标准化</label>
          <select id="pfStandardize" v-model="form.standardize">
            <option value="rank">rank（百分位秩）</option>
            <option value="zscore">zscore（z 分数）</option>
          </select></div>
      </div>
    </details>

    <details class="pf-section" id="pfSecCombine">
      <summary>③ 合成（多因子合成分数）</summary>
      <div class="pf-form-grid">
        <div class="pf-field"><label for="pfCombineMethod">合成方法</label>
          <select id="pfCombineMethod" v-model="form.combineMethod">
            <option value="equal_weight_rank">equal_weight_rank（等权秩基线）</option>
            <option value="rolling_ic_weight">rolling_ic_weight（滚动 IC 权重）</option>
          </select></div>
        <div class="pf-field" v-show="combineRowVisible"><label for="pfIcWindow">训练窗（年 1-10）</label><input type="number" id="pfIcWindow" v-model="form.icWindow" min="1" max="10" step="1"></div>
        <div class="pf-field" v-show="combineRowVisible"><label for="pfIcShrink">收缩系数（0-1）</label><input type="number" id="pfIcShrink" v-model="form.icShrink" step="any" min="0" max="1"></div>
        <div class="pf-field" v-show="combineRowVisible"><label for="pfIcMax">单因子权重上限（0-1）</label><input type="number" id="pfIcMax" v-model="form.icMax" step="any" min="0" max="1"></div>
        <div class="pf-field" v-show="combineRowVisible"><label for="pfIcFallback">训练不足退回</label>
          <select id="pfIcFallback" v-model="form.icFallback">
            <option value="equal_weight">equal_weight（退回等权）</option>
            <option value="cash">cash（保持现金）</option>
          </select></div>
      </div>
      <div class="hint">滚动 IC 权重只读取训练窗口数据，测试窗在结构上无法进入训练器（后端类型隔离）。</div>
    </details>

    <details class="pf-section" id="pfSecPolicy">
      <summary>④ 组合（分数 → 目标权重约束）</summary>
      <div class="pf-form-grid">
        <div class="pf-field"><label for="pfSelection">选股方式</label>
          <select id="pfSelection" v-model="form.selection">
            <option value="top_n">top_n（持仓数 N）</option>
            <option value="top_quantile">top_quantile（最高分位）</option>
          </select></div>
        <div class="pf-field" v-show="topNVisible"><label for="pfTopN">Top N 持仓数</label><input type="number" id="pfTopN" v-model="form.topN" min="1"></div>
        <div class="pf-field" v-show="topQuantileVisible"><label for="pfTopQuantile">分位（0-1）</label><input type="number" id="pfTopQuantile" v-model="form.topQuantile" step="any" min="0" max="1"></div>
        <div class="pf-field"><label for="pfMaxStockWeight">单票上限（0-1，0=不设限）</label><input type="number" id="pfMaxStockWeight" v-model="form.maxStockWeight" step="any" min="0" max="1"></div>
        <div class="pf-field"><label for="pfMaxIndustryWeight">行业上限（0-1，0=不设限）</label><input type="number" id="pfMaxIndustryWeight" v-model="form.maxIndustryWeight" step="any" min="0" max="1"></div>
        <div class="pf-field"><label for="pfMaxTurnover">换手上限（0-1，0=不设限）</label><input type="number" id="pfMaxTurnover" v-model="form.maxTurnover" step="any" min="0" max="1"></div>
        <div class="pf-field"><label for="pfMinHoldings">最小持仓数（≥0）</label><input type="number" id="pfMinHoldings" v-model="form.minHoldings" min="0" step="1"></div>
        <div class="pf-field"><label for="pfCashBuffer">现金缓冲（0-1）</label><input type="number" id="pfCashBuffer" v-model="form.cashBuffer" step="any" min="0" max="1"></div>
      </div>
    </details>

    <details class="pf-section" id="pfSecExec">
      <summary>⑤ 执行（可交易执行与成本）</summary>
      <div class="pf-form-grid">
        <div class="pf-field"><label for="pfRebalance">调仓频率</label>
          <select id="pfRebalance" v-model="form.rebalance">
            <option value="daily">daily（每日）</option>
            <option value="weekly">weekly（每周）</option>
            <option value="monthly">monthly（每月）</option>
          </select></div>
        <div class="pf-field"><label for="pfFillAt">成交时点</label>
          <select id="pfFillAt" v-model="form.fillAt">
            <option value="next_open">next_open（次日开盘）</option>
          </select></div>
        <div class="pf-field pf-check"><label for="pfSellFirst">先卖后买</label><input type="checkbox" id="pfSellFirst" v-model="form.sellFirst"></div>
        <div class="pf-field pf-check"><label for="pfT1">T+1 限制</label><input type="checkbox" id="pfT1" v-model="form.t1"></div>
        <div class="pf-field pf-check"><label for="pfCarryUnfilled">未成交跨日保留</label><input type="checkbox" id="pfCarryUnfilled" v-model="form.carryUnfilled"></div>
        <div class="pf-field"><label for="pfLotSize">整手股数</label><input type="number" id="pfLotSize" v-model="form.lotSize" min="1" max="10000" step="1"></div>
        <div class="pf-field"><label for="pfCommission">佣金费率（如 0.0003）</label><input type="number" id="pfCommission" v-model="form.commission" step="any" min="0"></div>
        <div class="pf-field"><label for="pfStampDuty">印花税率（如 0.001）</label><input type="number" id="pfStampDuty" v-model="form.stampDuty" step="any" min="0"></div>
        <div class="pf-field"><label for="pfTransferFee">过户费率（如 0）</label><input type="number" id="pfTransferFee" v-model="form.transferFee" step="any" min="0"></div>
        <div class="pf-field"><label for="pfSlippage">滑点（元/股）</label><input type="number" id="pfSlippage" v-model="form.slippage" step="any" min="0"></div>
        <div class="pf-field"><label for="pfMinCommission">最低佣金（元）</label><input type="number" id="pfMinCommission" v-model="form.minCommission" step="any" min="0"></div>
      </div>
    </details>

    <details class="pf-section" id="pfSecGates">
      <summary>⑥ 门禁（样本外验证规格；保存模型时冻结本分区，创建验证时提交）</summary>
      <div class="pf-form-grid">
        <div class="pf-field"><label for="pfTrainDays">训练窗（交易日）</label><input type="number" id="pfTrainDays" v-model="form.trainDays" min="1" step="1"></div>
        <div class="pf-field"><label for="pfTestDays">测试窗（交易日）</label><input type="number" id="pfTestDays" v-model="form.testDays" min="1" step="1"></div>
        <div class="pf-field"><label for="pfStep">步长（≥测试窗）</label><input type="number" id="pfStep" v-model="form.step" min="1" step="1"></div>
        <div class="pf-field"><label for="pfMinValidWindows">有效窗数下限（0=未配置）</label><input type="number" id="pfMinValidWindows" v-model="form.minValidWindows" min="0" step="1"></div>
        <div class="pf-field"><label for="pfMinTradingDays">有效交易日下限（0=未配置）</label><input type="number" id="pfMinTradingDays" v-model="form.minTradingDays" min="0" step="1"></div>
        <div class="pf-field"><label for="pfMinNetReturn">净收益下限（如 0.05）</label><input type="number" id="pfMinNetReturn" v-model="form.minNetReturn" step="any"></div>
        <div class="pf-field"><label for="pfMaxDrawdown">回撤边界（≤0，如 -0.15）</label><input type="number" id="pfMaxDrawdown" v-model="form.maxDrawdown" step="any"></div>
        <div class="pf-field"><label for="pfMaxCostDrag">成本拖累上限（如 0.03）</label><input type="number" id="pfMaxCostDrag" v-model="form.maxCostDrag" step="any" min="0"></div>
        <div class="pf-field"><label for="pfMinIR">信息比率下限（如 0.5）</label><input type="number" id="pfMinIR" v-model="form.minIR" step="any"></div>
        <div class="pf-field"><label for="pfMinExcessStability">超额为正窗占比下限（0-1）</label><input type="number" id="pfMinExcessStability" v-model="form.minExcessStability" step="any" min="0" max="1"></div>
        <div class="pf-field"><label for="pfMaxTurnoverGate">年换手上限（0-1）</label><input type="number" id="pfMaxTurnoverGate" v-model="form.maxTurnoverGate" step="any" min="0" max="1"></div>
        <div class="pf-field"><label for="pfMaxCashResidual">现金残留上限（0-1）</label><input type="number" id="pfMaxCashResidual" v-model="form.maxCashResidual" step="any" min="0" max="1"></div>
        <div class="pf-field"><label for="pfMaxUnfilled">未成交率上限（0-1）</label><input type="number" id="pfMaxUnfilled" v-model="form.maxUnfilled" step="any" min="0" max="1"></div>
        <div class="pf-field"><label for="pfMaxConcentration">单票集中度上限（0-1）</label><input type="number" id="pfMaxConcentration" v-model="form.maxConcentration" step="any" min="0" max="1"></div>
        <div class="pf-field"><label for="pfMinDirectionConsistency">净收益为正窗占比下限（0-1）</label><input type="number" id="pfMinDirectionConsistency" v-model="form.minDirectionConsistency" step="any" min="0" max="1"></div>
        <div class="pf-field"><label for="pfMinBaselineIncrement">相对等权基线增量下限</label><input type="number" id="pfMinBaselineIncrement" v-model="form.minBaselineIncrement" step="any"></div>
        <div class="pf-field"><label for="pfMaxDegradedWindows">允许降级窗口数上限（0=未配置）</label><input type="number" id="pfMaxDegradedWindows" v-model="form.maxDegradedWindows" min="0" step="1"></div>
      </div>
      <div class="hint">门禁阈值 0（或空）= 未配置该门禁（跳过并报告 unknown）；比例字段一律小数（0.5=50%）。最终结论（passed/failed/insufficient/error）由后端按冻结规格生成。</div>
    </details>

    <div class="editor-actions" style="margin-top:14px">
      <button type="button" class="primary" @click="savePfModel" :disabled="state.pfTaskRunning" :aria-busy="String(state.pfTaskRunning)">保存模型</button>
      <button type="button" class="secondary" @click="openPfExperimentDlg(state.pfEditModel)" :disabled="state.pfTaskRunning">创建实验…</button>
      <button type="button" class="secondary" @click="openPfValidationDlg(state.pfEditModel)" :disabled="state.pfTaskRunning">创建验证…</button>
      <button type="button" class="secondary" @click="closePfEdit">收起编辑</button>
      <span class="hint" style="margin:0">保存模型产生新 revision；创建实验/验证需要模型已保存（使用当前 revision 与 hash）。</span>
    </div>
    <div class="msg" :class="editMsg.ok ? 'ok' : 'err'" role="status" aria-live="polite">{{ editMsg.text }}</div>
  </div>

  <div class="pf-grid" style="margin-top:18px">
    <!-- 运行列表 -->
    <div class="card">
      <h3>运行列表 <span class="hint">服务端分页；行点击查看运行详情</span></h3>
      <div class="pf-toolbar">
        <button type="button" class="secondary" @click="loadPfExperiments(false)">刷新</button>
        <span class="pf-filters">
          <label for="pfExpFilterModel">模型 ID</label>
          <input type="text" id="pfExpFilterModel" v-model.trim="state.pfExpFilterModel" @input="pfExpFilterDebounced" placeholder="fm_…" aria-label="按模型 ID 筛选运行">
          <label for="pfExpFilterStatus">状态</label>
          <select id="pfExpFilterStatus" v-model="state.pfExpFilterStatus" @change="pfExpFilterStatusChange" aria-label="按运行状态筛选">
            <option value="">全部</option>
            <option value="queued">排队</option>
            <option value="running">运行中</option>
            <option value="completed">完成</option>
            <option value="failed">失败</option>
            <option value="cancelled">已取消</option>
            <option value="insufficient">证据不足</option>
          </select>
        </span>
      </div>
      <div class="warnbar" v-if="expErr" role="alert">{{ expErr }}</div>
      <template v-if="expItems.length">
        <div class="pf-table-wrap">
          <table id="pfExpTable">
            <thead><tr><th class="pf-idcol">实验 ID</th><th>状态</th><th>revision</th><th>研究区间</th><th>证据等级</th><th>进度</th><th>创建时间</th></tr></thead>
            <tbody id="pfExpBody">
              <tr v-for="exp in expItems" :key="exp.experimentId" :data-exp="exp.experimentId" style="cursor:pointer" @click="openPfRun(exp.experimentId)">
                <td class="pf-idcol">
                  <button type="button" class="pf-sortbtn" style="border:0;padding:0;margin:0;font-family:var(--font-num);font-size:12px;color:#9cc2ff;font-weight:600" @click.stop="openPfRun(exp.experimentId)">{{ exp.experimentId }}</button>
                </td>
                <td><span class="st-chip" :class="exp.status === 'completed' ? 'ready' : (exp.status === 'failed' || exp.status === 'cancelled') ? 'missing' : exp.status === 'insufficient' ? 'stale' : ''">{{ PF_RUNSTATE[exp.status] || exp.status || '—' }}</span></td>
                <td><span class="pf-num">v{{ exp.modelRevision }}</span></td>
                <td><span class="pf-num">{{ exp.studyRange && exp.studyRange.start ? exp.studyRange.start + '~' + exp.studyRange.end : '—' }}</span></td>
                <td v-html="pfEvidenceChip(exp.evidenceClass)"></td>
                <td><span class="pf-num">{{ (exp.progress || 0) + '%' }}</span></td>
                <td><span class="pf-num">{{ (exp.createdAt || '').replace('T', ' ').slice(0, 16) }}</span></td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>
      <div class="empty" v-else role="status">{{ state.pfExperiments && state.pfExperiments.total ? '本页没有运行记录。' : '暂无组合运行：在模型编辑中“创建实验”后启动。' }}</div>
      <div class="pf-pager" v-if="expPages > 1">
        <button type="button" @click="pfExpPageMove(-1)" :disabled="state.pfExpPage <= 1">上一页</button>
        <span>{{ expPageInfo }}</span>
        <button type="button" @click="pfExpPageMove(1)" :disabled="state.pfExpPage >= expPages">下一页</button>
      </div>
    </div>
    <!-- 验证列表 -->
    <div class="card">
      <h3>验证列表 <span class="hint">服务端分页；行点击查看验证详情</span></h3>
      <div class="pf-toolbar">
        <button type="button" class="secondary" @click="loadPfValidations(false)">刷新</button>
        <span class="pf-filters">
          <label for="pfValFilterModel">模型 ID</label>
          <input type="text" id="pfValFilterModel" v-model.trim="state.pfValFilterModel" @input="pfValFilterDebounced" placeholder="fm_…" aria-label="按模型 ID 筛选验证">
          <label for="pfValFilterVerdict">结论</label>
          <select id="pfValFilterVerdict" v-model="state.pfValFilterVerdict" @change="pfValFilterVerdictChange" aria-label="按验证结论筛选">
            <option value="">全部</option>
            <option value="passed">通过</option>
            <option value="failed">未通过</option>
            <option value="insufficient">证据不足</option>
            <option value="error">执行错误</option>
          </select>
        </span>
      </div>
      <div class="warnbar" v-if="valErr" role="alert">{{ valErr }}</div>
      <template v-if="valItems.length">
        <div class="pf-table-wrap">
          <table id="pfValTable">
            <thead><tr><th class="pf-idcol">验证 ID</th><th>结论</th><th>证据等级</th><th>revision</th><th>窗口规则</th><th>创建时间</th></tr></thead>
            <tbody id="pfValBody">
              <tr v-for="v in valItems" :key="v.id" :data-val="v.id" style="cursor:pointer" @click="openPfValDetail(v.id)">
                <td class="pf-idcol">
                  <button type="button" class="pf-sortbtn" style="border:0;padding:0;margin:0;font-family:var(--font-num);font-size:12px;color:#9cc2ff;font-weight:600" @click.stop="openPfValDetail(v.id)">{{ v.id }}</button>
                </td>
                <td><span class="st-chip" :class="v.verdict === 'passed' ? 'ready' : (v.verdict === 'failed' || v.verdict === 'error') ? 'missing' : v.verdict === 'insufficient' ? 'stale' : ''">{{ v.verdict === '进行中' ? '进行中' : (PF_VERDICT[v.verdict] || v.verdict || (v.state === 'completed' ? '—' : '进行中')) }}</span></td>
                <td v-html="pfEvidenceChip(v.evidenceClass)"></td>
                <td><span class="pf-num">v{{ v.modelRevision }}</span></td>
                <td><span class="pf-num">训{{ v.windowRule ? v.windowRule.trainDays : '—' }}/测{{ v.windowRule ? v.windowRule.testDays : '—' }}/步{{ v.windowRule ? v.windowRule.step : '—' }}</span></td>
                <td><span class="pf-num">{{ (v.createdAt || '').replace('T', ' ').slice(0, 16) }}</span></td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>
      <div class="empty" v-else role="status">{{ state.pfValidations && state.pfValidations.total ? '本页没有验证记录。' : '暂无组合验证：在模型编辑中“创建验证”后启动。' }}</div>
      <div class="pf-pager" v-if="valPages > 1">
        <button type="button" @click="pfValPageMove(-1)" :disabled="state.pfValPage <= 1">上一页</button>
        <span>{{ valPageInfo }}</span>
        <button type="button" @click="pfValPageMove(1)" :disabled="state.pfValPage >= valPages">下一页</button>
      </div>
    </div>
  </div>

  <!-- 运行详情 -->
  <div class="card" id="pfRunCard" v-show="runCardOpen" style="margin-top:18px">
    <h3 id="pfRunTitle" tabindex="-1">运行详情 · {{ selectedRunId }}<span class="pf-stale-tag" v-if="state.pfStale && state.activeTab===6">数据可能已过期</span></h3>
    <div class="pf-runstate" v-if="selectedRun">
      <span class="st-chip" :class="runStateCls">{{ runStateText }}</span>
      <span v-if="selectedRunStatus==='running'" class="pf-num">{{ selectedRun.progress || 0 }}%</span>
      <span v-if="selectedRunMessage" class="hint" style="margin:0">{{ selectedRunMessage }}</span>
      <div v-if="selectedRunError" class="msg err">{{ selectedRunError }}</div>
      <span style="display:inline-flex;gap:8px;margin-left:10px">
        <button v-if="selectedRunStatus==='queued'" type="button" class="primary" @click="startPfRun(selectedRunId)" :disabled="state.pfTaskRunning">启动运行</button>
        <button v-if="selectedRunStatus==='running'" type="button" class="danger" @click="pfStopRun(selectedRunId)">取消运行</button>
      </span>
    </div>
    <div class="empty" v-if="!selectedRunReport" role="status">{{ runLoadingText }}</div>
    <div class="warnbar" v-if="runErr" role="alert">{{ runErr }}</div>
    <div v-if="selectedRunReport">
      <div class="pf-detail-block">
        <h4>毛 / 净指标（后端报告口径）</h4>
        <div class="pf-metric-grid" v-html="runMetricsHtml"></div>
      </div>
      <div class="pf-detail-block">
        <h4>净值曲线（毛 / 净）</h4>
        <div ref="navChart" class="pf-nav-chart"></div>
      </div>
      <div class="pf-detail-block">
        <h4>风险与执行质量</h4>
        <div class="pf-metric-grid" v-html="runQualityHtml"></div>
        <div class="hint" v-if="runLimitations" style="margin-top:8px">{{ runLimitations }}</div>
      </div>
      <div class="pf-detail-block">
        <h4>目标 / 实际权重（最近交易日，偏离 = 实际 − 目标）</h4>
        <template v-if="weightRows.length">
          <div class="pf-table-wrap" style="max-height:320px">
            <table id="pfWeightTable">
              <thead><tr><th>代码</th><th>目标权重</th><th>实际权重</th><th>偏离</th></tr></thead>
              <tbody id="pfWeightBody">
                <tr v-for="w in weightRows" :key="w.code">
                  <td>{{ w.code }}</td>
                  <td>{{ pfPct(w.t) }}</td>
                  <td>{{ pfPct(w.a) }}</td>
                  <td :class="pfCls(w.dv)">{{ pfPct(w.dv) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </template>
        <div class="empty" v-else>该报告未保存逐日目标/实际权重明细。</div>
      </div>
      <div class="pf-detail-block">
        <h4>归因详情</h4>
        <div id="pfAttribution" v-html="attributionHtml"></div>
      </div>
      <div class="pf-detail-block">
        <h4>产物下载（仅 completed 可读，白名单五类）</h4>
        <div class="pf-artifacts" v-html="artifactsHtml"></div>
      </div>
    </div>
  </div>

  <!-- 验证详情 -->
  <div class="card" id="pfValDetailCard" v-show="valCardOpen" style="margin-top:18px">
    <h3 id="pfValDetailTitle" tabindex="-1">验证详情 · {{ state.pfSelectedValId }}<span class="pf-stale-tag" v-if="state.pfStale && state.activeTab===6">数据可能已过期</span></h3>
    <div class="empty" v-if="!state.pfSelectedVal" role="status">加载验证详情中…</div>
    <div class="warnbar" v-if="valDetailErr" role="alert">{{ valDetailErr }}</div>
    <div v-if="state.pfSelectedVal">
      <div :class="valVerdictCls">{{ valVerdictText }}</div>
      <div class="pf-runstate" v-html="valMetaHtml"></div>
      <div class="pf-detail-block">
        <h4>输入 hash 与冻结规格</h4>
        <div class="pf-hash" style="white-space:pre-wrap">{{ valHashesText }}</div>
      </div>
      <div class="pf-detail-block">
        <h4>逐窗门禁（窗口状态 / 指标 / 窗级门禁）</h4>
        <div class="pf-table-wrap" style="max-height:360px" v-html="valWindowsHtml"></div>
      </div>
      <div class="pf-detail-block">
        <h4>最终门禁（后端 Evaluate 输出）</h4>
        <div class="pf-table-wrap" style="max-height:360px" v-html="valGatesHtml"></div>
      </div>
      <div class="pf-detail-block">
        <h4>限制声明</h4>
        <div class="hint">{{ valLimitations }}</div>
      </div>
      <div class="editor-actions" style="margin-top:12px">
        <template v-if="state.pfSelectedVal.state === 'created'">
          <button type="button" class="primary" @click="startPfValidation(valRecId())" :disabled="state.pfTaskRunning">运行验证</button>
          <span class="hint" style="margin:0">长任务提交：服务端确认后按全局状态栏进度推进，窗口结果逐窗持久保存，断线可恢复。</span>
        </template>
        <span v-else class="hint" style="margin:0">验证已完成（不可重复运行）；如需修改模型或门禁，请创建新验证（自动标记旧验证为已见数据）。</span>
      </div>
    </div>
  </div>
</div>
  `,
};
