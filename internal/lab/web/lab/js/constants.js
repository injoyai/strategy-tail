// 全局常量与文案映射（与后端契约对应）。

// WAI-ARIA Tabs + URL 持久化：tab 编号 ↔ URL slug。
export const TAB_SLUG = {1: 'strategy', 2: 'compare', 3: 'trades', 4: 'factor', 5: 'candidates', 6: 'portfolio', 7: 'practice', 8: 'screen'};
export const TAB_NUM = {strategy: 1, compare: 2, trades: 3, factor: 4, candidates: 5, portfolio: 6, practice: 7, screen: 8};

export const TABS = [
  {n: 1, title: '回测配置'},
  {n: 2, title: '回测结果'},
  {n: 3, title: '交易明细'},
  {n: 4, title: '因子研究', navTitle: '因子分析'},
  {n: 5, title: '候选因子'},
  {n: 6, title: '组合研究'},
  {n: 7, title: '盘感练习'},
  {n: 8, title: '选股策略'},
];

export const NAV_GROUPS = [
  {id: 'backtest', title: '策略回测', tabs: [1, 2, 3]},
  {id: 'factor', title: '因子研究', tabs: [4, 5, 6], home: 4},
  {id: 'practice', title: '盘感练习', tabs: [7]},
  {id: 'screen', title: '选股策略', tabs: [8]},
];

export const TASK_LABEL = {backtest: '策略回测', analysis: '因子分析', portfolio: '组合运行', portfolio_validation: '组合验证', practice_prepare: '盘感练习出题', screen_history: '历史选股回算', screen_scan: '选股扫描'};

// 人工研究样本，不表示实时股息率排名或历史指数成员。
export const DIVIDEND_STOCKS = [
  {code: 'sh601398', name: '工商银行'},
  {code: 'sh601939', name: '建设银行'},
  {code: 'sh601288', name: '农业银行'},
  {code: 'sh601988', name: '中国银行'},
  {code: 'sh601328', name: '交通银行'},
  {code: 'sh601166', name: '兴业银行'},
  {code: 'sh601088', name: '中国神华'},
  {code: 'sh601225', name: '陕西煤业'},
  {code: 'sh600188', name: '兖矿能源'},
  {code: 'sh601898', name: '中煤能源'},
  {code: 'sh600900', name: '长江电力'},
  {code: 'sh600886', name: '国投电力'},
  {code: 'sh601006', name: '大秦铁路'},
  {code: 'sh600028', name: '中国石化'},
  {code: 'sh601857', name: '中国石油'},
  {code: 'sh600019', name: '宝钢股份'},
  {code: 'sh601668', name: '中国建筑'},
  {code: 'sz000895', name: '双汇发展'},
  {code: 'sh600941', name: '中国移动'},
  {code: 'sh601728', name: '中国电信'},
];
