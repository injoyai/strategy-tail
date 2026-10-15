// 运行配置构建：从 state.runCfg 读取，02 回测与 01 因子分析复用同一口径。
// 对应原 index.html 的 buildConfig()（读 DOM → 改读 store）。
import { state } from './store.js';

// 本次用户确定的账户规则，三种回测入口共用并随报告冻结。
export const ACCOUNT_CONFIG = Object.freeze({initialCapital: 100000, maxPositions: 5, orderBudget: 20000});

export function buildConfig() {
  const c = state.runCfg;
  const cfg = {
    startYear: +c.startYear,
    endYear: +c.endYear,
    sampleMode: c.sampleMode,
    holdingDays: c.holdingOn ? +c.holdingDays : 0,
    takeProfit: c.tpOn ? +c.takeProfit / 100 : 0,
    stopLoss: c.slOn ? +c.stopLoss / 100 : 0,
  };
  if (cfg.sampleMode === 'random') cfg.sampleSize = +c.sampleSize;
  if (cfg.sampleMode === 'codes') cfg.sampleCodes = c.sampleCodes.split(',').map(s => s.trim()).filter(Boolean);
  return cfg;
}
