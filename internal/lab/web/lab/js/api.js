// REST API 访问层：统一 fetch 封装 + 全部端点。非 2xx 抛出后端 error 文案。

export async function api(method, url, body) {
  const opt = {method, headers: {'Content-Type': 'application/json'}};
  if (body !== undefined) opt.body = JSON.stringify(body);
  const resp = await fetch(url, opt);
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error || resp.statusText);
  return data;
}

export const Api = {
  // 脚本编辑器 / 回测
  getScript: () => api('GET', '/api/script'),
  checkScript: content => api('POST', '/api/script/check', {content}),
  saveScript: content => api('PUT', '/api/script', {content}),
  runBacktest: cfg => api('POST', '/api/run', cfg),
  runStrategy: spec => api('POST', '/api/strategy/run', spec),
  stop: () => api('POST', '/api/stop'),
  status: () => api('GET', '/api/status'),
  latestReport: () => api('GET', '/api/report/latest'),
  kline: (code, from, to) => api('GET', `/api/kline/${encodeURIComponent(code)}?from=${from}&to=${to}`),
  // 因子研究
  factors: () => api('GET', '/api/factors'),
  analyze: req => api('POST', '/api/analyze', req),
  latestAnalysis: () => api('GET', '/api/analysis/latest'),
  // 简单模式预设
  strategyPresets: () => api('GET', '/api/strategy-presets'),
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
};
