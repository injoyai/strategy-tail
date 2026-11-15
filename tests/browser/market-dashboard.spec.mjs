import { expect, test } from '@playwright/test';

const top = Array.from({length: 25}, (_, i) => ({
  code: 'sh' + (600501 + i), close: 10 + i, change: 2 - i / 4, amount: (25 - i) * 1e8,
}));
const snapshot = {
  date: '2026-11-13', previousDate: '2026-11-12',
  indexes: [
    {code: 'sh000001', name: '上证指数', date: '2026-11-13', close: 3998.48, change: 0.36},
    {code: 'sz399001', name: '深证成指', date: '2026-11-12', close: 13019.84, change: -0.15},
    {code: 'bj899050', name: '北证50', date: '', change: null},
  ],
  stocks: {
    total: 28, up: 8, down: 16, flat: 2, unclassified: 2, amount: 325e8, withAmount: 25,
    changeDistribution: [1, 2, 8, 5, 3, 3, 1, 1],
    exchanges: [{exchange: 'sh', total: 28, amount: 325e8, up: 8, down: 16}],
    rankings: {amount: top, up: top.slice(0, 8), down: top.slice(9).reverse()},
  },
};

test('数据看板从菜单进入，支持改变榜单和名次，窄屏表格局部滚动', async ({page}) => {
  let requests = 0;
  await page.route('**/api/market-dashboard', route => {
    requests++;
    return route.fulfill({json: snapshot});
  });
  await page.route('**/api/stock-names', route => route.fulfill({json: {sh600501: '测试股票'}}));
  await page.goto('/');
  expect(requests).toBe(0);
  await page.getByRole('button', {name: '数据看板'}).click();
  await expect(page).toHaveURL(/tab=market-dashboard/);
  await expect(page.getByRole('heading', {level: 1, name: '数据看板'})).toBeVisible();
  await expect(page.getByText('3,998.48')).toBeVisible();
  await expect(page.getByRole('region', {name: '主要指数'}).getByText('暂无日线', {exact: true})).toBeVisible();
  await expect(page.getByText('成交额缺失 3 只', {exact: false})).toBeVisible();
  const movers = page.getByRole('region', {name: '个股涨 / 平 / 跌'});
  await expect(movers).toContainText(/上涨 8\s*\/\s*平盘 2\s*\/\s*下跌 16/);
  await expect(movers).toContainText('上涨率 30.8%');
  const distribution = page.getByRole('region', {name: '涨跌分布 / 广度'});
  await expect(distribution).toContainText('可比较 26 只');
  await expect(distribution).toContainText('缺少可比价格 2 只');
  const buckets = distribution.getByRole('listitem');
  await expect(buckets).toHaveCount(8);
  await expect(buckets.nth(2)).toHaveText(/^−3%～−1%\s*8 只$/);
  await expect(buckets.nth(4)).toHaveText(/^0%～1%\s*3 只$/);
  const barHeight = async index => (await buckets.nth(index).locator('.market-bucket-bar').boundingBox()).height;
  expect(await barHeight(2)).toBeGreaterThan(await barHeight(4));
  const table = page.getByRole('table', {name: '当前个股排行'});
  await expect(table.locator('tbody tr')).toHaveCount(10);
  await expect(table.getByRole('rowheader', {name: 'sh600501 测试股票'})).toBeVisible();
  const ranking = page.getByLabel('榜单类型');
  const limit = page.getByLabel('显示名次');
  await ranking.focus();
  await page.keyboard.press('Tab');
  await expect(limit).toBeFocused();
  await limit.selectOption('20');
  await expect(limit).toHaveValue('20');
  await expect(table.locator('tbody tr')).toHaveCount(20);
  await limit.selectOption('50');
  await expect(table.locator('tbody tr')).toHaveCount(25);
  await ranking.selectOption('down');
  await expect(table.locator('tbody tr').first()).toContainText('sh600525');
  await expect(table.getByRole('columnheader', {name: '涨跌幅'})).toHaveAttribute('aria-sort', 'ascending');
  expect(requests).toBe(1);

  await page.setViewportSize({width: 390, height: 844});
  await page.getByRole('button', {name: '菜单', exact: true}).press('Enter');
  await page.getByRole('button', {name: '数据看板'}).press('Enter');
  await expect(page.getByRole('button', {name: '菜单', exact: true})).toBeFocused();
  await expect(page.getByLabel('榜单类型')).toBeVisible();
  await expect(distribution).toBeVisible();
  expect((await buckets.nth(4).boundingBox()).y).toBeGreaterThan((await buckets.nth(0).boundingBox()).y);
  const scroller = page.getByRole('region', {name: '个股排行表格'});
  expect(await scroller.evaluate(el => el.scrollWidth > el.clientWidth)).toBe(true);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
});

test('数据看板加载、空日线、刷新失败和重试状态', async ({page}) => {
  let attempts = 0;
  let release;
  const pending = new Promise(resolve => { release = resolve; });
  await page.route('**/api/market-dashboard', async route => {
    switch (++attempts) {
      case 1:
        await pending;
        return route.fulfill({json: snapshot});
      case 2:
        return route.fulfill({status: 503, json: {error: '行情读取失败，请重试'}});
      default:
        return route.fulfill({json: {
          date: '', indexes: [], stocks: {total: 0, rankings: {amount: [], up: [], down: []}},
        }});
    }
  });
  await page.goto('/?tab=market-dashboard');
  await expect(page.getByText('正在读取行情…')).toBeVisible();
  await expect(page.getByText('正在读取分布数据')).toBeVisible();
  await expect(page.getByRole('button', {name: '刷新行情'})).toBeDisabled();
  release();
  await expect(page.getByText('3,998.48')).toBeVisible();
  await page.getByRole('button', {name: '刷新行情'}).click();
  await expect(page.getByRole('alert')).toContainText('仍显示上次读取的行情');
  await expect(page.getByText('3,998.48')).toBeVisible();
  await expect(page.getByText('上涨率 30.8%')).toBeVisible();
  await page.getByRole('button', {name: '重试读取'}).click();
  await expect(page.getByText('行情库暂无上证指数交易日记录。')).toBeVisible();
  await expect(page.getByText('暂无当日有效成交记录')).toBeVisible();
  await expect(page.getByText('暂无涨跌分布数据')).toBeVisible();
  await expect(page.getByText('当前榜单暂无数据')).toBeVisible();
});

test('交易时段自动刷新，切页暂停请求，返回时展示午间快照', async ({page}) => {
  let requests = 0;
  await page.clock.install();
  await page.route('**/api/market-dashboard', route => {
    const request = ++requests;
    const intraday = request < 4;
    return route.fulfill({json: {
      ...snapshot,
      source: intraday ? 'tdx' : 'daily',
      phase: request < 3 ? 'trading' : request === 3 ? 'break' : 'closed',
      capturedAt: intraday ? '2026-11-13T11:29:37+08:00' : undefined,
      refreshAfterMs: request < 3 ? 15000 : 3600000,
      stockQuotes: 25, stockUniverse: 28,
      indexes: [{...snapshot.indexes[0], close: 3900 + request}],
    }});
  });
  await page.goto('/?tab=market-dashboard');
  await expect(page.getByText('A 股实时行情', {exact: true})).toBeVisible();
  await expect(page.getByText('11:29:37', {exact: true})).toBeVisible();
  await expect(page.getByText('收到 25 / 28 只股票报价', {exact: false})).toBeVisible();
  await expect(page.getByRole('columnheader', {name: '最新价（元）'})).toBeVisible();
  await page.clock.fastForward(15000);
  await expect(page.getByText('3,902.00')).toBeVisible();
  await page.getByRole('button', {name: '策略回测', exact: true}).click();
  await page.clock.fastForward(30000);
  expect(requests).toBe(2);
  await page.getByRole('button', {name: '数据看板', exact: true}).click();
  await expect(page.getByText('午间休市', {exact: true})).toBeVisible();
  await expect(page.getByText('A 股盘中快照', {exact: true})).toBeVisible();
  await page.clock.fastForward(30000);
  expect(requests).toBe(3);
  await page.getByRole('button', {name: '刷新行情'}).click();
  await expect(page.getByText('A 股日线', {exact: true})).toBeVisible();
  await expect(page.getByRole('columnheader', {name: '收盘（元）'})).toBeVisible();
});
