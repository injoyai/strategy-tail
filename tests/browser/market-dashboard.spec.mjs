import { expect, test } from '@playwright/test';

const top = Array.from({length: 25}, (_, i) => ({
  code: 'sh' + (600501 + i), close: 10 + i, change: 2 - i / 4, amount: (25 - i) * 1e8,
}));
const reverse = [...top].reverse();
const snapshot = {
  date: '2026-11-13', previousDate: '2026-11-12',
  indexes: [
    {code: 'sh000001', name: '上证指数', date: '2026-11-13', close: 3998.48, change: 0.36},
    {code: 'sz399001', name: '深证成指', date: '2026-11-12', close: 13019.84, change: -0.15},
    {code: 'sz399006', name: '创业板指', date: '2026-11-13', close: 2426.08, change: -3.32},
    {code: 'sh000688', name: '科创50', date: '2026-11-13', close: 1279.66, change: -2.30},
    {code: 'sh000300', name: '沪深300', date: '2026-11-13', close: 4016.29, change: -0.68},
    {code: 'sh000905', name: '中证500', date: '2026-11-13', close: 6465.50, change: -2.10},
    {code: 'sh000852', name: '中证1000', date: '2026-11-13', close: 6357.26, change: -2.76},
    {code: 'bj899050', name: '北证50', date: '', change: null},
  ],
  stocks: {
    total: 28, up: 8, down: 16, flat: 2, unclassified: 2, amount: 325e8, withAmount: 25,
    changeDistribution: [1, 2, 8, 5, 3, 3, 1, 1],
    exchanges: [
      {exchange: 'sh', total: 14, amount: 160e8, up: 4, down: 8},
      {exchange: 'sz', total: 12, amount: 155e8, up: 3, down: 7},
      {exchange: 'bj', total: 2, amount: 10e8, up: 1, down: 1},
    ],
    rankings: {amount: top, amountAsc: reverse, changeDesc: top, changeAsc: reverse, closeDesc: reverse, closeAsc: top},
  },
};
const sectors = {
  date: '2026-11-13',
  industries: [
    {code: 'sh880101', name: '工程机械', change: 2.4, fiveDayRank: 7, fiveDaySum: 25},
    {code: 'sh880102', name: '电气设备', change: 1.9, fiveDayRank: 1, fiveDaySum: 9},
    {code: 'sh880103', name: '煤炭', change: 0.4},
    ...Array.from({length: 22}, (_, i) => ({code: 'sh' + (880110 + i), name: '行业板块 ' + (i + 4), change: -i / 10})),
  ],
  concepts: [
    {code: 'sh880201', name: '人形机器人', change: 3.1},
    {code: 'sh880202', name: '新型储能', change: 1.7},
    {code: 'sh880203', name: '第三代半导体', change: -0.5},
    ...Array.from({length: 15}, (_, i) => ({code: 'sh' + (880210 + i), name: '概念板块 ' + (i + 4), change: -1 - i / 10})),
  ],
  received: 43, expected: 45,
};

test.beforeEach(async ({page}) => {
  await page.route('**/api/market-dashboard/sectors', route => route.fulfill({json: sectors}));
});

test('两张榜单可表内滚动，表头固定，窄屏无横向溢出', async ({page}) => {
  let requests = 0;
  await page.setViewportSize({width: 1280, height: 720});
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
  const indexes = page.getByRole('region', {name: '主要指数'});
  await expect(indexes.locator('.market-index-name strong')).toHaveText(['上证指数', '深证成指', '创业板指', '沪深300']);
  await expect(indexes.getByText('2026-11-13', {exact: true})).toBeVisible();
  await expect(indexes.getByText('2026-11-12', {exact: true})).toBeVisible();
  await expect(page.getByRole('button', {name: '刷新行情'})).toHaveCount(0);
  await expect(page.getByText('成交额缺失 3 只', {exact: false})).toBeVisible();
  const distribution = page.getByRole('region', {name: '涨跌分布'});
  await expect(distribution).toContainText(/上涨\s*8/);
  await expect(distribution).toContainText(/平盘\s*2/);
  await expect(distribution).toContainText(/下跌\s*16/);
  await expect(distribution).toContainText(/上涨率\s*30\.8%/);
  await expect(distribution).toContainText('可比较 26 只');
  await expect(distribution).toContainText('缺少可比价格 2 只');
  const buckets = distribution.getByRole('list', {name: '各涨跌幅区间股票数'}).getByRole('listitem');
  await expect(buckets).toHaveCount(8);
  await expect(buckets.nth(2)).toHaveText(/^−3%～−1%\s*8 只$/);
  await expect(buckets.nth(4)).toHaveText(/^0%～1%\s*3 只$/);
  const barHeight = async index => (await buckets.nth(index).locator('.market-bucket-bar').boundingBox()).height;
  expect(await barHeight(2)).toBeGreaterThan(await barHeight(4));
  const sectorRegion = page.getByRole('region', {name: '热门板块'});
  await expect(sectorRegion).toContainText(/通达信\s*2026-11-13/);
  await expect(sectorRegion).toContainText('覆盖 43 / 45');
  const sectorScroll = sectorRegion.getByRole('region', {name: '板块排行滚动区'});
  const industryTable = sectorRegion.getByRole('table', {name: '行业涨幅排行'});
  await expect(industryTable.locator('tbody tr')).toHaveCount(25);
  await expect(industryTable.getByRole('columnheader', {name: '五日排名'})).toBeVisible();
  for (const width of [1280, 1440]) {
    await page.setViewportSize({width, height: 720});
    expect(await sectorScroll.evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true);
    for (const cell of await industryTable.locator('thead th:first-child, thead th:nth-child(3), thead th:last-child, tbody tr:first-child td').all()) {
      expect(await cell.evaluate(el => el.scrollWidth <= el.clientWidth && getComputedStyle(el).whiteSpace === 'nowrap')).toBe(true);
    }
  }

  await expect(industryTable.locator('tbody tr').nth(0).locator('td').last()).toHaveText('7');
  await expect(industryTable.locator('tbody tr').nth(1).locator('td').last()).toHaveText('1');
  await expect(industryTable.locator('tbody tr').nth(2).locator('td').last()).toHaveText('—');
  await sectorScroll.focus();
  await sectorScroll.press('End');
  await expect(industryTable.getByRole('rowheader', {name: '行业板块 25'})).toBeInViewport();
  await expect(industryTable.getByRole('columnheader', {name: '板块', exact: true})).toBeInViewport();
  const conceptButton = sectorRegion.getByRole('button', {name: '概念', exact: true});
  await conceptButton.click();
  await expect(conceptButton).toHaveAttribute('aria-pressed', 'true');
  const conceptTable = sectorRegion.getByRole('table', {name: '概念涨幅排行'});
  await expect(conceptTable.locator('tbody tr')).toHaveCount(18);
  await expect(conceptTable.locator('tbody tr').first()).toHaveText(/sh880201 人形机器人\s*\+3\.10%/);
  expect(await sectorScroll.evaluate(el => el.scrollTop)).toBe(0);
  const table = page.getByRole('table', {name: '当前个股排行'});
  await expect(table.locator('tbody tr')).toHaveCount(25);
  await expect(table.getByRole('rowheader', {name: 'sh600501 测试股票'})).toBeVisible();
  await expect(table.getByRole('button', {name: '查看 sh600501 测试股票 日K线'})).toHaveClass(/secondary.*trade-kline-button/);
  expect(await page.evaluate(() => document.documentElement.scrollHeight)).toBeLessThanOrEqual(720);
  const stockScroll = page.getByRole('region', {name: '个股排行滚动区'});
  const close = table.getByRole('columnheader', {name: '收盘（元）'});
  const change = table.getByRole('columnheader', {name: '涨跌幅'});
  const amount = table.getByRole('columnheader', {name: '成交额（亿元）'});
  await expect(amount).toHaveAttribute('aria-sort', 'descending');
  await stockScroll.focus();
  await page.keyboard.press('Tab');
  await expect(close.getByRole('button')).toBeFocused();
  await stockScroll.press('End');
  await expect(table.locator('tbody tr').last()).toBeInViewport();
  await expect(amount).toBeInViewport();
  await expect(table.locator('tbody tr').first()).not.toBeInViewport();
  await amount.getByRole('button').click();
  expect(await stockScroll.evaluate(el => el.scrollTop)).toBe(0);
  await expect(amount).toHaveAttribute('aria-sort', 'ascending');
  await expect(table.locator('tbody tr').first()).toContainText('sh600525');
  await expect(table.locator('tbody td').first()).toHaveText('1');
  for (const [header, descending, ascending] of [
    [amount, 'sh600501', 'sh600525'],
    [change, 'sh600501', 'sh600525'],
    [close, 'sh600525', 'sh600501'],
  ]) {
    await header.getByRole('button').click();
    await expect(header).toHaveAttribute('aria-sort', 'descending');
    await expect(table.locator('tbody tr').first()).toContainText(descending);
    await header.getByRole('button').click();
    await expect(header).toHaveAttribute('aria-sort', 'ascending');
    await expect(table.locator('tbody tr').first()).toContainText(ascending);
  }
  await expect(amount).not.toHaveAttribute('aria-sort', /.+/);
  await expect(change).not.toHaveAttribute('aria-sort', /.+/);
  await expect(page.getByLabel('榜单类型')).toHaveCount(0);
  await expect(page.getByLabel('显示名次')).toHaveCount(0);
  await expect(page.getByRole('navigation', {name: '个股排行分页'})).toHaveCount(0);
  expect(requests).toBe(1);

  await page.setViewportSize({width: 390, height: 844});
  await page.getByRole('button', {name: '菜单', exact: true}).press('Enter');
  await page.getByRole('button', {name: '数据看板'}).press('Enter');
  await expect(page.getByRole('button', {name: '菜单', exact: true})).toBeFocused();
  await expect(change.getByRole('button')).toBeVisible();
  await expect(distribution).toBeVisible();
  expect((await buckets.nth(4).boundingBox()).y).toBeGreaterThan((await buckets.nth(0).boundingBox()).y);
  for (const width of [390, 320]) {
    await page.setViewportSize({width, height: 844});
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
    for (const cell of await industryTable.locator('thead th:first-child, thead th:nth-child(3), thead th:last-child, tbody tr:first-child td').all()) {
      expect(await cell.evaluate(el => el.scrollWidth <= el.clientWidth && getComputedStyle(el).whiteSpace === 'nowrap')).toBe(true);
    }
    for (const frame of [stockScroll, sectorScroll]) {
      expect(await frame.evaluate(el => el.scrollWidth <= el.clientWidth && el.scrollHeight > el.clientHeight)).toBe(true);
    }
  }
});

test('数据看板加载、空日线、刷新失败和重试状态', async ({page}) => {
  let attempts = 0;
  await page.clock.install();
  let release;
  const pending = new Promise(resolve => { release = resolve; });
  await page.route('**/api/market-dashboard', async route => {
    switch (++attempts) {
      case 1:
        await pending;
        return route.fulfill({json: {...snapshot, phase: 'trading', refreshAfterMs: 15000}});
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
  await expect(page.getByRole('button', {name: '刷新行情'})).toHaveCount(0);
  release();
  await expect(page.getByText('3,998.48')).toBeVisible();
  await page.clock.fastForward(15000);
  await expect(page.getByRole('alert')).toContainText('仍显示上次读取的行情');
  await expect(page.getByText('3,998.48')).toBeVisible();
  await expect(page.getByRole('region', {name: '涨跌分布'})).toContainText(/上涨率\s*30\.8%/);
  await page.getByRole('button', {name: '重试读取'}).click();
  await expect(page.getByText('行情库暂无上证指数交易日记录。')).toBeVisible();
  await expect(page.getByText('暂无当日有效成交记录')).toBeVisible();
  await expect(page.getByText('暂无涨跌分布数据')).toBeVisible();
  await expect(page.getByText('当前榜单暂无数据')).toBeVisible();
});

test('行业与概念可打开各自日K，关闭后保留所选类别和焦点', async ({page}) => {
  const requests = [];
  const bars = [
    {date: '2026-11-12', open: 100, high: 104, low: 98, close: 102, volume: 5000, dif: 0, dea: 0, macd: 0},
    {date: '2026-11-13', open: 102, high: 108, low: 101, close: 107, volume: 4800, dif: 1, dea: 0.5, macd: 0.5},
  ];
  await page.route('**/api/market-dashboard', route => route.fulfill({json: snapshot}));
  await page.route('**/api/kline/**', route => {
    requests.push(new URL(route.request().url()).pathname);
    return route.fulfill({json: bars});
  });
  await page.goto('/?tab=market-dashboard');
  const sector = page.getByRole('region', {name: '热门板块'});
  const scroll = sector.getByRole('region', {name: '板块排行滚动区'});
  const concept = sector.getByRole('button', {name: '概念', exact: true});
  for (const {code, name} of [sectors.industries[0], sectors.concepts.at(-1)]) {
    const entry = sector.getByRole('button', {name: `查看 ${code} ${name} 日K线`});
    await expect(entry).toHaveText(`${code} ${name}`);
    await entry.focus();
    const top = await scroll.evaluate(el => el.scrollTop);
    await entry.press('Enter');
    const dialog = page.getByRole('dialog', {name: `${code} ${name} · 日K线`});
    const chart = dialog.locator('.chart-kline');
    await expect(chart.locator('canvas').first()).toBeVisible();
    await expect.poll(() => chart.evaluate(el => window.klinecharts.init(el).getDataList().at(-1)?.close)).toBe(107);
    await page.keyboard.press('Escape');
    await expect(dialog).toBeHidden();
    await expect(entry).toBeFocused();
    expect(await scroll.evaluate(el => el.scrollTop)).toBe(top);
    if (code === sectors.industries[0].code) await concept.click();
  }
  await expect(concept).toHaveAttribute('aria-pressed', 'true');
  expect(requests).toEqual(['/api/kline/sh880101', '/api/kline/sh880224']);
});

test('交易时段自动刷新，切页暂停请求，返回时展示午间快照', async ({page}) => {
  let requests = 0;
  let sectorRequests = 0;
  await page.clock.install();
  await page.setViewportSize({width: 1280, height: 720});
  await page.route('**/api/market-dashboard/sectors', route => {
    switch (++sectorRequests) {
      case 2:
        return route.fulfill({status: 503, json: {error: '板块行情暂不可用'}});
      case 3:
        return route.fulfill({json: {...sectors, industries: [{...sectors.industries[0], change: 4.25}]}});
      default:
        return route.fulfill({json: sectors});
    }
  });
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
      stocks: request === 2
        ? {...snapshot.stocks, rankings: {...snapshot.stocks.rankings, amount: top.slice(0, 20)}}
        : snapshot.stocks,
    }});
  });
  await page.goto('/?tab=market-dashboard');
  await expect(page.getByText('实时行情', {exact: true})).toBeVisible();
  await expect(page.getByText('11:29:37', {exact: true})).toBeVisible();
  await expect(page.getByText('收到 25 / 28 只股票报价', {exact: false})).toBeVisible();
  const sectorRegion = page.getByRole('region', {name: '热门板块'});
  await expect(sectorRegion.getByText('+2.40%', {exact: true})).toBeVisible();
  await expect(page.getByRole('columnheader', {name: '最新价（元）'})).toBeVisible();
  const rankTable = page.getByRole('table', {name: '当前个股排行'});
  await expect(rankTable.locator('tbody tr')).toHaveCount(25);
  const stockScroll = page.getByRole('region', {name: '个股排行滚动区'});
  await stockScroll.evaluate(el => { el.scrollTop = 100; });
  await page.clock.fastForward(15000);
  await expect(page.getByText('3,902.00')).toBeVisible();
  await expect(sectorRegion.getByRole('status')).toHaveText('板块行情暂未更新，显示上次报价。');
  await expect(sectorRegion.getByText('+2.40%', {exact: true})).toBeVisible();
  await expect(rankTable.locator('tbody tr')).toHaveCount(20);
  await expect(rankTable.locator('tbody td').first()).toHaveText('1');
  expect(await stockScroll.evaluate(el => el.scrollTop)).toBe(100);
  await page.getByRole('button', {name: '策略回测', exact: true}).click();
  await page.clock.fastForward(30000);
  expect(requests).toBe(2);
  expect(sectorRequests).toBe(2);
  await page.getByRole('button', {name: '数据看板', exact: true}).click();
  await expect(page.getByText('午间休市', {exact: true})).toBeVisible();
  await expect(page.getByText('盘中快照', {exact: true})).toBeVisible();
  await expect(sectorRegion.getByText('+4.25%', {exact: true})).toBeVisible();
  await page.clock.fastForward(30000);
  expect(requests).toBe(3);
  await page.clock.fastForward(3600000);
  await expect(page.getByText('日线', {exact: true})).toBeVisible();
  await expect(page.getByRole('columnheader', {name: '收盘（元）'})).toBeVisible();
});
