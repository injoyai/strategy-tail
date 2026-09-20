// CandidateEvidenceDlg：候选证据只读摘要对话框（05 页“证据”触发）。
// 迁移自 index.html viewCandidateEvidence。
import { dlgMixin } from './dlgMixin.js';

export default {
  name: 'CandidateEvidenceDlg',
  mixins: [dlgMixin],
  data() {
    return {lines: []};
  },
  methods: {
    openDlg(c) {
      const ev = c.evidence || {};
      const r = ev.range || {};
      this.lines = [
        ['分析 ID', ev.analysisId || '—'],
        ['分析版本', String(ev.analysisVersion || 0)],
        ['证据 SHA-256', ev.reportSha256 ? ev.reportSha256.slice(0, 16) + '…' : '—'],
        ['未来收益窗口', ev.window ? ev.window + ' 个交易日' : '—'],
        ['分析区间', r.startYear ? `${r.startYear}–${r.endYear}` : '—'],
        ['样本', (r.sampleMode || '—') + (r.sampleSize ? ` · ${r.sampleSize} 只` : '')],
        ['实际有效日期', ev.firstDataDate && ev.lastDataDate ? `${ev.firstDataDate} ~ ${ev.lastDataDate}` : '—'],
        ['IC 有效日 / 均值', ev.stats ? `${ev.stats.pairs} 日 / ${ev.stats.mean.toFixed(4)}` : '—'],
        ['完成时间', ev.finishedAt || '—'],
      ];
      this.open = true;
    },
  },
  template: `
<dialog ref="dlg" aria-labelledby="candEvTitle">
  <div class="dlg-head">
    <h3 id="candEvTitle">候选证据</h3>
    <button class="dlg-close" @click="closeDlg" aria-label="关闭">×</button>
  </div>
  <div id="candEvBody" style="font-size:12px;line-height:1.9;color:var(--ink2)">
    <div v-for="l in lines" :key="l[0]"><b>{{ l[0] }}：</b>{{ l[1] }}</div>
  </div>
  <div class="hint" style="margin-top:10px">证据是样本内研究记录，不可变保存；不代表样本外验证通过。</div>
</dialog>
  `,
};
