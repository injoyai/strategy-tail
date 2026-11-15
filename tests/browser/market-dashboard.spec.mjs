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
        return route.fulfill({status: 503, json: {error: '日线行情读取失败，请重试'}});
      default:
        return route.fulfill({json: {
          date: '', indexes: [], stocks: {total: 0, rankings: {amount: [], up: [], down: []}},
        }});
    }
  });
  await page.goto('/?tab=market-dashboard');
  await expect(page.getByText('正在读取日线行情…')).toBeVisible();
  await expect(page.getByRole('button', {name: '刷新行情'})).toBeDisabled();
  release();
  await expect(page.getByText('3,998.48')).toBeVisible();
  await page.getByRole('button', {name: '刷新行情'}).click();
  await expect(page.getByRole('alert')).toContainText('仍显示上次读取的日线');
  await expect(page.getByText('3,998.48')).toBeVisible();
  await page.getByRole('button', {name: '重试读取'}).click();
  await expect(page.getByText('行情库暂无上证指数交易日记录。')).toBeVisible();
  await expect(page.getByText('暂无当日有效成交记录')).toBeVisible();
  await expect(page.getByText('当前榜单暂无数据')).toBeVisible();
});
