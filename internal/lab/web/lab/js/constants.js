// 全局常量与文案映射（与后端契约对应）。

// WAI-ARIA Tabs + URL 持久化：tab 编号 ↔ URL slug。
export const TAB_SLUG = {1: 'strategy', 2: 'compare', 3: 'trades', 4: 'factor', 5: 'candidates', 6: 'portfolio', 7: 'practice'};
export const TAB_NUM = {strategy: 1, compare: 2, trades: 3, factor: 4, candidates: 5, portfolio: 6, practice: 7};

export const TABS = [
  {n: 1, title: '回测配置'},
  {n: 2, title: '回测结果'},
  {n: 3, title: '交易明细'},
  {n: 4, title: '因子研究'},
  {n: 5, title: '候选因子'},
  {n: 6, title: '组合研究'},
  {n: 7, title: '盘感练习'},
];

export const NAV_GROUPS = [
  {id: 'backtest', title: '策略回测', tabs: [1, 2, 3]},
  {id: 'factor', title: '因子研究', tabs: [4, 5, 6]},
  {id: 'practice', title: '盘感练习', tabs: [7]},
];

export const TASK_LABEL = {backtest: '策略回测', data_update: '数据更新', analysis: '因子分析', portfolio: '组合运行', portfolio_validation: '组合验证', practice_prepare: '盘感练习出题'};
