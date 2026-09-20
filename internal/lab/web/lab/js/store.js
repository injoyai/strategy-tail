// 全局响应式状态（跨 Tab 共享的数据与 UI 状态）。
// ECharts 实例、定时器句柄等非序列化对象不放入 reactive：图表实例由各
// 组件以普通变量持有，轮询句柄与跨轮询记忆放 runtime。

import { reactive } from './vendor/vue.esm-browser.prod.js';

export const state = reactive({
  // —— 全局 / 状态轮询 ——
  activeTab: 4,          // 当前 Tab 编号；默认进入 01 因子研究
  status: null,          // 最近一次 /api/status 响应
  statusError: false,    // 轮询网络失败 → connection-error 提示条
  taskRunning: false,    // 任务运行中（按钮禁用 / 状态条进度）

  // —— 02 策略与运行 ——
  runMode: 'simple',     // simple | adv
  runCfg: {              // 运行配置（02 右侧卡片；01 因子分析复用样本配置，仅覆盖年份）
    startYear: 2025,
    endYear: 2026,
    sampleMode: 'all',   // all | random | codes
    sampleSize: 50,
    sampleCodes: '',
    holdingOn: true,
    holdingDays: 1,
    tpOn: false,
    takeProfit: 10,
    slOn: false,
    stopLoss: 8,
  },
  scriptMsg: {text: '', ok: true},   // 02 消息条
  factorMsg: {text: '', ok: true},   // 01 消息条
  focusCondReq: null,                // {kind, days}：跨页定位条件卡请求（02 页消费）。
                                     // 约定：写入方先 push simpleConds → switchTab(1) → 再写本字段，
                                     // 保证 02 面板可见后才能聚焦。
  stopBusy: false,                   // 停止任务按钮 busy

  // —— 02 简单配置（跨页共享：01/05 会向条件列表追加条件） ——
  simpleConds: [],                   // 条件卡数据源 {kind, days, operator, min, max, factorVersion, err, errField}；
                                     // min/max 为显示值（ratio 因子按百分比），提交时转原始比例
  condsInitialized: false,           // 默认条件卡只创建一次（因子目录就绪后）
  presetId: '',                      // 基础策略预设选择（''=不使用预设）
  specName: '',                      // 策略名称输入

  // —— 批量任务卡（状态条；polling.js 写入，根组件渲染） ——
  taskDock: null,

  // —— 03 组合对比 / 04 交易明细（共享报告） ——
  currentReport: null,
  selectedVariant: null, // 03 行点击 → 04 筛选

  // —— 01 因子研究 ——
  factorCatalog: [],
  currentAnalysis: null,
  currentGroupingN: 5,               // 结果区当前展示的分组数（跨分析保留上次切换档）
  currentChartScope: 'overall',      // overall | annual | year:YYYY（跨分组切换保留当前视角）

  // —— 05 候选因子 ——
  candidates: [],
  showCandidatesArchived: false,
  candidateRequestId: null,          // 保存请求幂等：输入变化时重置
  candidateSavedFor: null,           // 已保存内容指纹（重复保存显示“已保存”）
  editCandidate: null,               // 编辑对话框目标（null=新建）
  editExpectedRevision: null,        // 乐观锁：打开编辑时的 revision
  candidatesDirty: 0,                // 候选数据版本号：保存/编辑成功后递增，05 页监听刷新

  // —— 06 组合研究 ——
  pfModels: [],                      // 模型列表（服务端当前页，最新 revision）
  pfModelPageData: null,             // GET /api/factor-models 分页响应（items/total/page/pageSize）
  pfEvidenceModels: [],              // 全量模型（仅供“输入证据”聚合；表格仍走服务端分页）
  pfEvidenceLoaded: false,
  pfModelLoading: false,
  pfModelPage: 1,
  pfModelPageSize: 8,
  pfModelFilterId: '',
  pfModelFilterState: '',
  pfModelSort: {key: 'createdAt', dir: 'desc'},
  pfLastValCache: {},                // modelId -> {verdict, id, createdAt} 最近验证缓存
  pfEditModel: null,                 // 编辑表单当前模型（null=新建）
  pfSelectedModelId: '',
  pfValidatedFactors: [],            // 聚合的可选已验证因子（带 validationId）
  pfSelectedValidationIds: [],
  pfModelRequestId: null,            // 模型保存幂等：输入变化时重置
  pfCandidates: [],
  pfExperiments: null,               // ExperimentPage
  pfExpPage: 1,
  pfExpPageSize: 8,
  pfExpFilterModel: '',
  pfExpFilterStatus: '',
  pfValidations: null,               // PortfolioValidationPage
  pfValPage: 1,
  pfValPageSize: 8,
  pfValFilterModel: '',
  pfValFilterVerdict: '',
  pfSelectedRun: null,               // 选中的实验（详情）
  pfSelectedRunReport: null,
  pfSelectedVal: null,               // 选中的验证（详情）
  pfSelectedValId: '',
  pfStale: false,                    // 后台刷新期间旧内容标记 stale
  pfSeq: 0,                          // 详情请求序列号：快速连点时旧响应丢弃
  pfTaskRunning: false,              // 组合任务运行中（06 页按钮禁用）
  pfExpRequestId: null,              // 实验创建幂等
  pfValRequestId: null,              // 验证创建幂等
  pfExpDirty: 0,                     // 实验数据版本号：创建成功后递增，06 页监听刷新
  pfValDirty: 0,                     // 验证数据版本号：创建成功后递增，06 页监听刷新
});

// 非响应式运行时句柄（定时器、跨轮询记忆）。
export const runtime = {
  pollTimer: null,
  prevRunning: false,   // 用于识别“任务被停止”（running → idle）
  lastTask: '',         // 最近一次启动的任务类型：backtest | analysis | portfolio | portfolio_validation
};
