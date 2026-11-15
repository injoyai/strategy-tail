import { expect, test } from '@playwright/test';

const kind = 'turnover_per_price_move';

for (const width of [1440, 390]) {
  test(`${width}px 研究和回测通过同一目录选择每1%涨跌成交额`, async ({page}) => {
    await page.setViewportSize({width, height: 900});
    await page.route('https://cdn.jsdelivr.net/**', route => route.fulfill({contentType: 'text/javascript', body: ''}));
    await page.goto('/?tab=factor');

    const research = page.getByLabel('因子', {exact: true});
    const option = research.locator(`optgroup[label="规模与流动性"] option[value="${kind}"]`);
    await expect(option).toHaveText('每1%涨跌成交额');
    await research.selectOption(kind);
    await expect(research).toHaveValue(kind);
    await expect(page.getByLabel('统计天数')).toHaveValue('1');
    const holding = page.getByLabel('持有周期', {exact: true});
    await expect(holding).toHaveValue('1');
    await expect(page.locator('#holdingSessionsHelp')).toContainText('入场后的下一交易日收盘');
    await holding.fill('0');
    await page.getByRole('button', {name: '开始分析'}).click();
    await expect(page.getByText('持有周期应为 1 至 60 个交易日')).toBeVisible();
    await expect(holding).toBeFocused();
    await holding.fill('7');
    await page.route('**/api/analyze', route => route.fulfill({
      status: 409, json: {error: '测试行情读取失败'},
    }));
    const submitted = page.waitForRequest(req => req.method() === 'POST' && req.url().endsWith('/api/analyze'));
    await page.getByRole('button', {name: '开始分析'}).click();
    const cfg = (await submitted).postDataJSON();
    expect(cfg).toMatchObject({kind, days: 1, holdingSessions: 7});
    expect(cfg).not.toHaveProperty('window');
    await expect(page.getByText('测试行情读取失败')).toBeVisible();
    await expect(holding).toHaveValue('7');
    await page.locator('#factorConcept summary').click();
    await expect(page.locator('#fcExample')).toContainText('5000 万元');
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);

    await page.goto('/?tab=strategy');
    await page.getByRole('button', {name: '添加因子买入条件'}).click();
    const builder = page.locator('.bnode-factor-fields');
    await builder.getByLabel('因子', {exact: true}).selectOption(kind);
    await expect(builder.getByLabel('统计天数')).toHaveValue('1');
    await builder.getByLabel('比较', {exact: true}).selectOption('lte');
    const maximum = builder.getByLabel(/^上限\s*亿元\/百分点$/);
    await maximum.fill('0.5');
    await expect(maximum).toHaveAttribute('aria-invalid', 'false');
    await expect(maximum).toHaveValue('0.5');
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);

    await page.getByRole('button', {name: '因子组合', exact: true}).click();
    await page.getByLabel('条件 1 因子', {exact: true}).selectOption(kind);
    await expect(page.getByLabel('条件 1 下限（亿元/百分点）', {exact: true})).toBeVisible();
  });
}

test('展示新报告的持有期、分组覆盖和 HAC 统计', async ({page}) => {
  await page.setViewportSize({width: 390, height: 900});
  await page.route('https://cdn.jsdelivr.net/**', route => route.fulfill({contentType: 'text/javascript', body: ''}));
  await page.route('**/api/status', route => route.fulfill({json: {state: 'done', task: 'analysis'}}));
  const groups = [
    {label: 'Q1', factorMean: 1, observations: 10, labeled: 8, countPct: 0.5, forwardReturn: 0.01},
    {label: 'Q2', factorMean: 2, observations: 10, labeled: 10, countPct: 0.5, forwardReturn: -0.01},
  ];
  const ic = {pairs: 15, mean: -0.05, std: 0.16, hacTStat: -1.23, hacLag: 7};
  const stats = {...ic, tStat: -2};
  await page.route('**/api/analysis/latest', route => route.fulfill({json: {
    analysisVersion: 4, window: 7, range: {startYear: 2025, endYear: 2025, sampleMode: 'all', sampleSize: 2},
    grouping: {groups: 2}, groups, years: [{year: 2025, stats, groups}],
    stats, signalCoverage: {valid: 20, observed: 25},
    horizonAnalyses: [{
      label: 'next_open_held_sessions', ic, years: [{year: 2025, ic}],
      labelCoverage: {labeledSignals: 18, eligibleSignals: 20, missingExitBar: 2},
    }],
  }}));
  await page.goto('/?tab=factor');
  await expect(page.locator('#factorEvidence')).toContainText('探索性毛收益');
  await expect(page.locator('#rangeMeta')).toContainText('沪深主板日线库');
  await expect(page.locator('#factorLabelCoverage')).toContainText('退出日缺行情 2');
  await expect(page.locator('#groupDetail')).toContainText('80.0%');
  await expect(page.locator('#yearStability')).toContainText('HAC t 值');
  await page.getByText('统计详情（IC、显著性、逐日 IC）').click();
  await expect(page.locator('#icStats')).toContainText('Newey–West t 值 -1.23');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
