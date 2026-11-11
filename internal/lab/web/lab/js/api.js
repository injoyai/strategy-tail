// REST API 访问层：统一 fetch 封装 + 全部端点。非 2xx 抛出后端 error 文案。

export async function api(method, url, body) {
  const opt = {method, headers: {'Content-Type': 'application/json'}};
  if (body !== undefined) opt.body = JSON.stringify(body);
  const resp = await fetch(url, opt);
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) {
    const err = new Error(data.error || resp.statusText);
    err.status = resp.status;
    throw err;
  }
  return data;
}

export const Api = {
  stockNames: () => api('GET', '/api/stock-names'),
  stockInfo: (code, asOf = '') => api('GET', '/api/stock-info/' + encodeURIComponent(code) + (asOf ? '?asOf=' + encodeURIComponent(asOf) : '')),
  macdConfig: () => api('GET', '/api/macd/config'),
  runMACD: cfg => api('POST', '/api/macd/run', cfg),
  marketDataStatus: () => api('GET', '/api/market-data/status'),
  // 脚本编辑器 / 回测
  getScript: () => api('GET', '/api/script'),
  checkScript: content => api('POST', '/api/script/check', {content}),
  saveScript: content => api('PUT', '/api/script', {content}),
  runBacktest: cfg => api('POST', '/api/run', cfg),
  runStrategy: spec => api('POST', '/api/strategy/run', spec),
  stop: () => api('POST', '/api/stop'),
  status: () => api('GET', '/api/status'),
  latestReport: () => api('GET', '/api/report/latest'),
  reports: () => api('GET', '/api/reports'),
  report: id => api('GET', '/api/report/' + encodeURIComponent(id)),
  async exportReport(id) {
    const response = await fetch('/api/report/' + encodeURIComponent(id) + '/export.xlsx');
    if (!response.ok) {
      const data = await response.json().catch(() => ({}));
      throw new Error(data.error || '下载失败，请重试');
    }
    const url = URL.createObjectURL(await response.blob()), link = document.createElement('a');
    link.href = url; link.download = 'report-' + id + '.xlsx';
    document.body.append(link); link.click(); link.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  },
  deleteReport: id => api('DELETE', '/api/report/' + encodeURIComponent(id)),
  kline: (code, from, to) => api('GET', `/api/kline/${encodeURIComponent(code)}?from=${from}&to=${to}`),
  // 回放（报告内快照，旧报告不可用时 400 提示重新回测）
  replay: (id, variant) => api('GET', `/api/report/${encodeURIComponent(id)}/replay/${variant}`),
  replayKline: (id, code) => api('GET', `/api/report/${encodeURIComponent(id)}/replay/kline/${encodeURIComponent(code)}`),
  // 因子研究
  factors: () => api('GET', '/api/factors'),
  analyze: req => api('POST', '/api/analyze', req),
  latestAnalysis: () => api('GET', '/api/analysis/latest'),
  // 简单模式预设
  strategyPresets: () => api('GET', '/api/strategy-presets'),
  // 策略搭建器（自由组合条件树）
  builderComponents: () => api('GET', '/api/builder/components'),
  builderTemplates: () => api('GET', '/api/builder/templates'),
  builderStrategies: () => api('GET', '/api/builder/strategies'),
  builderStrategy: id => api('GET', '/api/builder/strategies/' + encodeURIComponent(id)),
  createBuilderStrategy: spec => api('POST', '/api/builder/strategies', {spec}),
  updateBuilderStrategy: (id, spec, expectedVersion) =>
    api('PUT', '/api/builder/strategies/' + encodeURIComponent(id), {spec, expectedVersion}),
  copyBuilderStrategy: (id, name) =>
    api('POST', '/api/builder/strategies/' + encodeURIComponent(id) + '/copy', {name}),
  runBuilder: cfg => api('POST', '/api/builder/run', cfg),
  // 候选因子
  candidates: includeArchived => api('GET', '/api/factor-candidates' + (includeArchived ? '?includeArchived=true' : '')),
  candidate: id => api('GET', '/api/factor-candidates/' + encodeURIComponent(id)),
  createCandidate: req => api('POST', '/api/factor-candidates', req),
  updateCandidate: (id, req) => api('PUT', '/api/factor-candidates/' + encodeURIComponent(id), req),
  // 组合研究：因子模型
  factorModels: query => api('GET', '/api/factor-models' + (query || '')),
  factorModel: id => api('GET', '/api/factor-models/' + encodeURIComponent(id)),
  saveFactorModel: req => api('POST', '/api/factor-models', req),
  // 组合研究：实验 / 产物 / 运行 / 验证
  portfolioExperiments: query => api('GET', '/api/portfolio-experiments' + (query || '')),
  createPortfolioExperiment: req => api('POST', '/api/portfolio-experiments', req),
  experiment: id => api('GET', '/api/portfolio-experiments/' + encodeURIComponent(id)),
  experimentArtifact: (id, name) => api('GET', `/api/portfolio-experiments/${encodeURIComponent(id)}/artifacts/${name}`),
  startPortfolioRun: id => api('POST', '/api/portfolio-runs/' + encodeURIComponent(id) + '/start'),
  stopPortfolioRun: id => api('POST', '/api/portfolio-runs/' + encodeURIComponent(id) + '/stop'),
  portfolioValidations: query => api('GET', '/api/portfolio-validations' + (query || '')),
  createPortfolioValidation: req => api('POST', '/api/portfolio-validations', req),
  validation: id => api('GET', '/api/portfolio-validations/' + encodeURIComponent(id)),
  practiceBanks: () => api('GET', '/api/practice-banks'),
  createPracticeBank: req => api('POST', '/api/practice-banks', req),
  practiceBank: id => api('GET', '/api/practice-banks/' + encodeURIComponent(id)),
  trainings: () => api('GET', '/api/practice-trainings'),
  startTraining: req => api('POST', '/api/practice-trainings', req),
  training: id => api('GET', '/api/practice-trainings/' + encodeURIComponent(id)),
  trainingAction: (id, cmd) => api('POST', '/api/practice-trainings/' + encodeURIComponent(id) + '/actions', cmd),
  trainingReport: id => api('GET', '/api/practice-trainings/' + encodeURIComponent(id) + '/report'),
  trainingQuestion: (id, q) => api('GET', '/api/practice-trainings/' + encodeURIComponent(id) + '/questions/' + encodeURIComponent(q)),
  trainingReview: (id, q) => api('GET', '/api/practice-trainings/' + encodeURIComponent(id) + '/questions/' + encodeURIComponent(q) + '/review'),
  trainingRetry: (id, q, requestId) => api('POST', '/api/practice-trainings/' + encodeURIComponent(id) + '/questions/' + encodeURIComponent(q) + '/retry', {requestId}),
  // 盘感练习
  practiceStart: req => api('POST', '/api/practice', req),
  practices: () => api('GET', '/api/practices'),
  practice: id => api('GET', '/api/practice/' + encodeURIComponent(id)),
  practiceAction: (id, cmd) => api('POST', '/api/practice/' + encodeURIComponent(id) + '/actions', cmd),
  practiceReview: id => api('GET', '/api/practice/' + encodeURIComponent(id) + '/review'),
  practiceRetry: (id, requestId) => api('POST', '/api/practice/' + encodeURIComponent(id) + '/retry', {requestId}),
};
