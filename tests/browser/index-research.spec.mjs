import { expect, test } from '@playwright/test';

const indexes = [{code: 'sh000001', name: '上证指数'}, {code: 'sh000300', name: '沪深300'}];
const points = [
  {date: '2024-01-02', close: 3000, concentration: null},
  {date: '2024-01-03', close: 3080, concentration: {share: 0.25, selected: 250, shanghai: 2200, shenzhen: 2500, beijing: 300, excluded: 5}},
  {date: '2024-01-04', close: 3097.25, concentration: {share: 0.2745, selected: 251, shanghai: 2210, shenzhen: 2500, beijing: 300, excluded: 0}},
];

test('指数研究默认绘图并按用户规则重新查询，窄屏菜单可达', async ({page}) => {
  const requests = [];
  await page.route('**/api/index-research/indexes', route => route.fulfill({json: indexes}));
  await page.route('**/api/index-research/series?*', route => {
    requests.push(Object.fromEntries(new URL(route.request().url()).searchParams));
    return route.fulfill({json: points});
  });
  await page.goto('/');
  expect(requests).toHaveLength(0);
  await page.getByRole('button', {name: '指数研究'}).click();
  await expect(page).toHaveURL(/tab=index-research/);
  await expect(page.getByRole('heading', {level: 1, name: '指数研究'})).toBeVisible();
  await expect(page.getByText('27.45%')).toBeVisible();
  expect(requests[0]).toEqual({index: 'sh000001', from: '2000-01-01', to: expect.stringMatching(/^\d{4}-\d{2}-\d{2}$/), topPercent: '5'});
  await expect(page.getByLabel('起始日期')).toHaveValue('2000-01-01');
  await expect(page.getByLabel('排序字段')).toHaveCount(0);
  await expect(page.getByLabel('统计占比')).toHaveCount(0);
  const chart = page.getByRole('group', {name: /指数收盘点位和成交拥挤度曲线/});
  await expect(chart.locator('canvas').first()).toBeVisible();
  await chart.focus();
  await page.keyboard.press('ArrowLeft');
  await expect(page.getByText('2024-01-03 · 收盘 3,080.00 点 · 拥挤度 25.00%')).toBeVisible();
  expect(await chart.evaluate(el => window.echarts.getInstanceByDom(el).getOption().dataZoom[0].xAxisIndex)).toEqual([0, 1]);

  await page.getByLabel('成交额前 N%').fill('0');
  await page.getByRole('button', {name: '绘制曲线'}).click();
  await expect(page.getByRole('alert')).toContainText('1 至 100');
  await expect(page.getByLabel('成交额前 N%')).toBeFocused();
  expect(requests).toHaveLength(1);
  const indexSelect = page.getByLabel('对照指数');
  await indexSelect.focus();
  await page.keyboard.press('Space');
  await page.keyboard.press('Escape');
  await expect(indexSelect).toBeFocused();
  await indexSelect.selectOption('sh000300');
  await page.getByLabel('起始日期').fill('2024-01-02');
  await page.getByLabel('结束日期').fill('2024-01-04');
  await page.getByLabel('成交额前 N%').fill('10');
  await page.getByRole('button', {name: '绘制曲线'}).click();
  await expect(page.getByText('成交额前 10% 的股票占当日样本成交额的比例', {exact: false})).toBeVisible();
  expect(requests[1]).toEqual({index: 'sh000300', from: '2024-01-02', to: '2024-01-04', topPercent: '10'});

  await page.setViewportSize({width: 390, height: 844});
  await page.getByRole('button', {name: '菜单', exact: true}).press('Enter');
  await page.getByRole('button', {name: '指数研究'}).press('Enter');
  await expect(page.getByRole('button', {name: '菜单', exact: true})).toBeFocused();
  await expect(page.getByRole('button', {name: '绘制曲线'})).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
});

test('指数目录失败可重试，缺少日线时显示空态', async ({page}) => {
  let attempts = 0;
  await page.route('**/api/index-research/indexes', route => ++attempts === 1
    ? route.fulfill({status: 503, json: {error: '指数目录读取失败，请重试'}})
    : route.fulfill({json: indexes}));
  let release;
  const pending = new Promise(resolve => { release = resolve; });
  await page.route('**/api/index-research/series?*', async route => {
    await pending;
    await route.fulfill({json: []});
  });
  await page.goto('/?tab=index-research');
  await expect(page.getByRole('alert')).toContainText('指数目录读取失败');
  await page.getByRole('button', {name: '重试读取'}).click();
  await expect(page.getByText('正在读取日线并计算成交拥挤度…')).toBeVisible();
  await expect(page.getByRole('button', {name: '绘制曲线'})).toBeDisabled();
  release();
  await expect(page.getByText('所选区间无指数收盘数据')).toBeVisible();
  await expect(page.getByText('所选区间无有效成交拥挤度数据')).toBeVisible();
});
