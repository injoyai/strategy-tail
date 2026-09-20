// 全局常量与文案映射（与后端契约对应）。

// WAI-ARIA Tabs + URL 持久化：tab 编号 ↔ URL slug。
export const TAB_SLUG = {1: 'strategy', 2: 'compare', 3: 'trades', 4: 'factor', 5: 'candidates', 6: 'portfolio'};
export const TAB_NUM = {strategy: 1, compare: 2, trades: 3, factor: 4, candidates: 5, portfolio: 6};

export const TABS = [
  {n: 4, index: '01', title: '因子研究', desc: '观察分组 · 形成条件'},
  {n: 1, index: '02', title: '策略与运行', desc: '配置假设 · 发起回测'},
  {n: 2, index: '03', title: '组合对比', desc: '读取差异 · 审视贡献'},
  {n: 3, index: '04', title: '交易明细', desc: '回到逐笔 · 核对路径'},
  {n: 5, index: '05', title: '候选因子', desc: '证据 · 修订 · 复用'},
  {n: 6, index: '06', title: '组合研究', desc: '模型 · 运行 · 验证'},
];

export const TASK_LABEL = {backtest: '策略回测', analysis: '因子分析', portfolio: '组合运行', portfolio_validation: '组合验证'};

// 组合任务阶段文案（/api/status phase；Task 9 契约）
export const PF_PHASE = {
  queued: '排队中',
  loading_data: '加载数据',
  transform: '截面变换',
  combine: '合成分数',
  target: '目标组合',
  execute: '组合执行',
  metrics: '组合指标',
  attribution: '收益归因',
  finalize: '发布产物',
};
