// 全局常量与文案映射（与后端契约对应）。

// WAI-ARIA Tabs + URL 持久化：tab 编号 ↔ URL slug。
export const TAB_SLUG = {1: 'strategy', 2: 'compare', 3: 'trades', 4: 'factor', 5: 'candidates', 6: 'portfolio'};
export const TAB_NUM = {strategy: 1, compare: 2, trades: 3, factor: 4, candidates: 5, portfolio: 6};

export const TABS = [
  {n: 1, title: '回测配置'},
  {n: 2, title: '回测结果'},
  {n: 3, title: '交易明细'},
  {n: 4, title: '因子研究'},
  {n: 5, title: '候选因子'},
  {n: 6, title: '组合研究'},
];

export const TASK_LABEL = {backtest: '策略回测', data_update: '数据更新', analysis: '因子分析', portfolio: '组合运行', portfolio_validation: '组合验证'};
