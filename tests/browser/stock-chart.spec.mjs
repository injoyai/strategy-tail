import { expect, test } from '@playwright/test';

const first = {code: 'sz300308', close: 466.20, change: -8.39, amount: 258.74e8};
const second = {code: 'sh688256', close: 978.88, change: 0.60, amount: 160.38e8};
const bars = [
  {date: '2026-11-12', open: 501, high: 512, low: 500, close: 509, volume: 5000, dif: 0, dea: 0, macd: 0},
  {date: '2026-11-13', open: 500, high: 501, low: 460, close: 466.20, volume: 5200, dif: -2, dea: -1, macd: -1},
];

test.beforeEach(async ({page}) => {
  await page.route('**/api/market-dashboard/sectors', route => route.fulfill({status: 503, json: {error: '板块行情暂不可用'}}));
  await page.route('**/api/stock-names', route => route.fulfill({json: {
    [first.code]: '中际旭创', [second.code]: '寒武纪',
  }}));
  await page.route('**/api/market-dashboard', route => route.fulfill({json: {
    date: '2026-11-13', indexes: [], stocks: {
      total: 2, up: 1, down: 1, flat: 0, amount: first.amount + second.amount, withAmount: 2,
      rankings: {amount: [first, second], changeDesc: [second, first], changeAsc: [first, second]}, exchanges: [],
    },
  }}));
});

test('股票代码和名称在当前榜单打开日K，键盘关闭后保留榜单和焦点', async ({page}, testInfo) => {
  const requests = [];
  await page.route('**/api/kline/**', route => {
    requests.push(new URL(route.request().url()).pathname);
    return route.fulfill({json: bars});
  });
  await page.goto('/?tab=market-dashboard');
  const change = page.getByRole('columnheader', {name: '涨跌幅'});
  await change.getByRole('button').click();
  await change.getByRole('button').click();
  const stock = page.getByRole('button', {name: '查看 sz300308 中际旭创 日K线'});
  await stock.press('Enter');
  const dialog = page.getByRole('dialog', {name: 'sz300308 中际旭创 · 日K线'});
  const chart = dialog.locator('.chart-kline');
  await expect(chart.locator('canvas').first()).toBeVisible();
  await expect.poll(() => chart.evaluate(el => window.klinecharts.init(el).getDataList().at(-1)?.close)).toBe(466.20);
  expect(requests).toEqual(['/api/kline/sz300308']);
  await expect(page).toHaveURL(/tab=market-dashboard$/);
  await expect(dialog.getByRole('button', {name: '关闭'})).toBeFocused();
  await dialog.screenshot({path: testInfo.outputPath('stock-chart.png')});
  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();
  await expect(stock).toBeFocused();
  await expect(change).toHaveAttribute('aria-sort', 'ascending');
});

test('窄屏日K弹窗支持失败重试和无行情，关闭后可查看另一股', async ({page}, testInfo) => {
  await page.setViewportSize({width: 390, height: 844});
  let attempts = 0;
  let release;
  const pending = new Promise(resolve => { release = resolve; });
  await page.route('**/api/kline/sz300308?*', async route => {
    if (++attempts === 1) {
      await pending;
      return route.fulfill({status: 503, json: {error: '日线读取失败'}});
    }
    return route.fulfill({json: []});
  });
  await page.route('**/api/kline/sh688256?*', route => route.fulfill({json: bars}));
  await page.goto('/?tab=market-dashboard');
  await page.getByRole('button', {name: /查看 sz300308/}).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog.getByRole('status')).toHaveText('正在加载日K线…');
  release();
  await expect(dialog.getByRole('alert')).toHaveText('日线读取失败');
  await dialog.getByRole('button', {name: '重试'}).click();
  await expect(dialog.getByRole('status')).toHaveText('该标的暂无日K数据。');
  await expect(dialog.locator('canvas')).toHaveCount(0);
  await dialog.getByRole('button', {name: '关闭'}).click();
  await page.getByRole('button', {name: /查看 sh688256/}).click();
  await expect(dialog.getByRole('heading')).toHaveText('sh688256 寒武纪 · 日K线');
  await expect(dialog.locator('canvas').first()).toBeVisible();
  const box = await dialog.boundingBox();
  expect(box.x).toBeGreaterThan(0);
  expect(box.x + box.width).toBeLessThanOrEqual(390);
  expect(box.y + box.height).toBeLessThanOrEqual(844);
  await dialog.screenshot({path: testInfo.outputPath('stock-chart.png')});
});

test('组合权重与股票贡献使用同一个日K弹窗', async ({page}) => {
  const experimentId = 'pe_chart';
  await page.route('**/api/portfolio-experiments/' + experimentId, route => route.fulfill({json: {
    experiment: {experimentId, status: 'completed', manifest: {}},
  }}));
  await page.route('**/api/portfolio-experiments/' + experimentId + '/artifacts/report.json', route => route.fulfill({json: {
    attribution: {
      weightSeries: [{target: {[first.code]: 0.3}, actual: {[first.code]: 0.25}}],
      stocks: [{code: second.code, contribution: 0.01}],
    },
  }}));
  await page.route('**/api/kline/**', route => route.fulfill({json: bars}));
  await page.goto('/?tab=portfolio&run=' + experimentId);
  const dialog = page.getByRole('dialog');
  for (const [selector, label] of [['#pfWeightTable', 'sz300308 中际旭创'], ['#pfAttribution', 'sh688256 寒武纪']]) {
    const trigger = page.locator(selector).getByRole('button', {name: '查看 ' + label + ' 日K线'});
    await trigger.press('Enter');
    await expect(dialog.getByRole('heading')).toHaveText(label + ' · 日K线');
    await expect(dialog.locator('canvas').first()).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(dialog).toBeHidden();
    await expect(trigger).toBeFocused();
    await expect(page).toHaveURL(/run=pe_chart/);
  }
});
