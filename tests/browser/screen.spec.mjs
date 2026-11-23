import { test, expect } from '@playwright/test';

test.beforeEach(async ({page}) => {
  await page.route('https://cdn.jsdelivr.net/**', route => route.fulfill({contentType: 'text/javascript', body: ''}));
});

test('配置多套策略、合并命中、按策略查看并保留配置', async ({page}) => {
  await page.goto('/?tab=screen');
  await expect(page.getByRole('heading', {level: 1})).toHaveText('选股策略');
  const library = page.getByRole('region', {name: '策略列表'});
  await expect(library).toBeVisible();
  await expect(library.getByRole('button', {name: '全部策略', exact: true})).toHaveCount(0);
  await expect(library.locator('input')).toHaveCount(0);
  await expect(library.getByRole('button', {name: /^(运行|已启用|未启用)/})).toHaveCount(0);
  await expect(library.getByRole('button', {name: '立即选股'})).toBeEnabled();
  await expect(library.getByRole('button', {name: '下载提醒工具'})).toBeVisible();

  // Keep the actual service for config writes. The browser fixture has no market
  // attached, so inject two synthetic hits for the results presentation.
  const own = ['价格筛选 A', '价格筛选 B'];
  for (const name of own) {
    await library.getByRole('button', {name: '新建策略'}).click();
    const dlg = page.getByRole('dialog');
    await dlg.getByRole('textbox', {name: '策略名称'}).fill(name);
    const save = dlg.getByRole('button', {name: '保存策略'});
    await save.click();
    await expect(dlg.getByRole('alert')).toHaveText('请添加条件或删除空分组');
    await dlg.getByRole('button', {name: '+ 条件', exact: true}).click();
    const component = dlg.getByRole('combobox', {name: /选择组件/});
    await component.selectOption('buy_price');
    await dlg.getByRole('spinbutton', {name: /最低价/}).fill('5');
    await expect(dlg.getByRole('checkbox', {name: '保存后启用'})).toHaveCount(0);
    await save.click();
    await expect(dlg).toBeHidden();
  }

  await page.route('**/api/screen', async route => {
    if (route.request().method() !== 'GET') return route.continue();
    const response = await route.fetch();
    const payload = await response.json();
    const custom = payload.config.strategies.filter(strategy => strategy.buy && !strategy.disabled);
    await route.fulfill({response, json: {...payload, running: false, error: '',
      result: {asOf: '2026-11-13 10:00:00', universe: 1, evaluated: 1, noSessionBars: 0, failed: 0,
        matches: custom.length ? [{code: 'sh600000', name: '浦发银行', price: 12, rise: 1.25, time: '2026-11-13 09:59:00',
          hits: custom.map(strategy => ({id: strategy.id}))}] : []}}});
  });
  await page.reload();
  const table = page.getByRole('table', {name: /选股结果/});
  const firstStrategy = library.getByRole('button', {name: '价格筛选 A', exact: true});
  await expect(library.locator('[aria-current="true"]')).toHaveCount(0);
  await expect(table.getByRole('row')).toHaveCount(2);
  await expect(table).toContainText('价格筛选 A');
  await expect(table).toContainText('价格筛选 B');
  await expect(table).toContainText('+1.25%');
  await firstStrategy.click();
  await expect(table.getByRole('row')).toHaveCount(2);
  await expect(table).not.toContainText('价格筛选 B');

  const strategySwitch = library.getByRole('switch', {name: '启用策略 价格筛选 A'});
  await expect(strategySwitch).toBeChecked();
  const screenURL = '**/api/screen';
  const rejectSave = route => route.request().method() === 'PUT'
    ? route.fulfill({status: 409, json: {error: '测试配置冲突'}})
    : route.fallback();
  await page.route(screenURL, rejectSave);
  await strategySwitch.click();
  await expect(page.getByRole('alert')).toHaveText('测试配置冲突');
  await expect(strategySwitch).toBeChecked();
  await page.unroute(screenURL, rejectSave);
  await expect(strategySwitch).toBeEnabled();
  await strategySwitch.focus();
  await page.keyboard.press('Space');
  await expect(strategySwitch).not.toBeChecked();
  await expect(firstStrategy).toHaveAttribute('aria-current', 'true');
  await expect(page.getByText('当前策略已禁用。')).toBeVisible();
  await page.reload();
  await expect(strategySwitch).not.toBeChecked();
  await strategySwitch.click();
  await expect(strategySwitch).toBeChecked();
  await firstStrategy.click();

  await page.getByRole('button', {name: '编辑 价格筛选 A'}).click();
  let dlg = page.getByRole('dialog');
  await expect(dlg.getByRole('spinbutton', {name: /最低价/})).toHaveValue('5');
  await dlg.getByRole('textbox', {name: '策略名称'}).fill('修改后放弃');
  await page.keyboard.press('Escape');
  await expect(dlg.getByRole('button', {name: '继续编辑'})).toBeFocused();
  await dlg.getByRole('button', {name: '放弃修改'}).click();
  await expect(dlg).toBeHidden();
  await expect(page.getByRole('button', {name: '编辑 价格筛选 A'})).toBeFocused();

  await page.getByRole('button', {name: '删除 价格筛选 A'}).click();
  dlg = page.getByRole('dialog');
  await expect(dlg.getByRole('button', {name: '取消'})).toBeFocused();
  await dlg.getByRole('button', {name: '删除策略'}).click();
  await expect(dlg).toBeHidden();
  await expect(firstStrategy).toHaveCount(0);
  await expect(library.locator('[aria-current="true"]')).toHaveCount(0);
  await page.getByRole('button', {name: '编辑 价格筛选 B'}).click();
  await expect(dlg.getByRole('checkbox', {name: '保存后启用'})).toHaveCount(0);
  await dlg.getByRole('button', {name: '保存策略'}).click();
  await expect(dlg).toBeHidden();
  await page.getByRole('button', {name: '删除 价格筛选 B'}).click();
  await page.getByRole('dialog').getByRole('button', {name: '删除策略'}).click();
  await expect(page.getByRole('dialog')).toBeHidden();
});

test('窄屏菜单、旧深链、状态失败与重试', async ({page}) => {
  await page.setViewportSize({width: 390, height: 844});
  let fail = true;
  await page.route('**/api/screen', route => fail
    ? route.fulfill({status: 503, json: {error: '测试读取失败'}})
    : route.continue());
  await page.goto('/?tab=trades');
  await expect(page.getByRole('heading', {level: 1})).toHaveText('交易明细');
  const menu = page.getByRole('button', {name: '菜单', exact: true});
  await menu.click();
  const nav = page.getByRole('navigation', {name: '主导航'});
  await expect(nav.getByRole('button', {name: '策略回测'})).toBeFocused();
  await nav.getByRole('button', {name: '选股策略'}).click();
  await expect(nav).toBeHidden();
  await expect(menu).toBeFocused();
  await expect(page).toHaveURL(/tab=screen$/);
  await expect(page.getByText(/读取选股状态失败/)).toBeVisible();
  fail = false;
  await page.getByRole('button', {name: '重试读取'}).click();
  const library = page.getByRole('region', {name: '策略列表'});
  await expect(library).toBeVisible();
  await expect(page.getByRole('button', {name: '策略列表', exact: true})).toHaveCount(0);
  await library.getByRole('button', {name: '新建策略'}).press('Enter');
  const dlg = page.getByRole('dialog');
  await dlg.getByRole('button', {name: '+ 条件', exact: true}).click();
  await expect(dlg.getByRole('combobox', {name: /选择组件/})).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  expect(await dlg.evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true);
});

for (const width of [1440, 390]) {
  test(`${width}px 买点、卖点和历史通过共用 K 线查看`, async ({page}) => {
    await page.setViewportSize({width, height: 900});
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    const strategy = {id: 'macd-premium', name: 'MACD精选'};
    const buyTime = '2026-11-12 15:00:00', sellTime = '2026-11-13 10:00:00';
    const signal = {code: 'sh600000', name: '浦发银行', strategy: strategy.id, buyTime, sellTime,
      buyPrice: 10, sellPrice: 11, lastPrice: 12, rate: 10, sellSource: 'minute', tags: ['涨停^']};
    const klines = [{date: '2026-11-12', open: 9, close: 10, high: 10, low: 9, volume: 100, dif: 0, dea: 0, macd: 0},
      {date: '2026-11-13', open: 10, close: 12, high: 12, low: 10, volume: 110, dif: 0.1, dea: 0.02, macd: 0.08}];
    await page.route('**/api/screen', route => route.fulfill({json: {running: false,
      config: {version: 1, strategies: [strategy]}, result: {asOf: sellTime, universe: 1,
        evaluated: 1, noSessionBars: 0, failed: 0,
        matches: [{code: signal.code, name: signal.name, time: sellTime, price: 12, rise: 20, hits: [{id: strategy.id}]}]}}}));
    await page.route('**/api/screen/history?*', route => route.fulfill({json: {
      rows: [signal], total: 1, page: 1, pages: 1, lookback: 60, through: '2026-11-13', running: false,
      avgHoldingDays: 1, stats: {total: 1, winRate: 100, profitFactor: null},
    }}));
    let diagnosisFailed = true;
    await page.route('**/api/screen/diagnose/**', route => {
      if (diagnosisFailed) return route.fulfill({status: 503, json: {error: '测试行情读取失败'}});
      return route.fulfill({json: {code: signal.code, name: signal.name, asOf: sellTime, klines, signals: [signal],
        rules: [{name: 'MACD精选', matched: false, children: [{name: '均线条件', matched: true}]}]}});
    });
    await page.goto('/?tab=screen');
    const buy = page.getByRole('button', {name: '查看 sh600000 浦发银行 K线', exact: true});
    const historyFilters = page.locator('.screen-history-filters');
    const toolbar = page.locator('.screen-results-toolbar');
    await expect(buy).toBeVisible();
    await expect(historyFilters).toBeHidden();
    await buy.click();
    const dialog = page.getByRole('dialog');
    await expect(dialog.getByRole('alert')).toContainText('测试行情读取失败');
    diagnosisFailed = false;
    await dialog.getByRole('button', {name: '重试'}).click();
    await expect(dialog.getByRole('heading')).toContainText('浦发银行');
    await expect(dialog.locator('.screen-kline canvas').first()).toBeVisible();
    await expect(dialog.getByText('均线条件')).toBeVisible();
    await dialog.getByRole('button', {name: '查看全部K线'}).click();
    await page.keyboard.press('Escape');
    await expect(buy).toBeFocused();

    const sell = page.getByRole('tab', {name: '当日卖点'});
    await sell.click();
    await expect(historyFilters).toBeVisible();
    await expect(buy).toBeHidden();
    await expect(toolbar.getByRole('textbox')).toHaveCount(1);
    await expect(toolbar.getByRole('button', {name: '查看K线', exact: true})).toHaveCount(0);
    const table = page.locator('.screen-signal-table');
    await expect(table).toHaveClass(/tabulator/);
    await expect(table).toContainText('分钟线');
    await table.getByRole('button', {name: /查看 sh600000 浦发银行/}).click();
    await expect(dialog.locator('.screen-kline canvas').first()).toBeVisible();
    await dialog.getByRole('button', {name: '关闭', exact: true}).click();

    await sell.focus();
    await page.keyboard.press('ArrowRight');
    await expect(page.getByRole('tab', {name: '历史交易'})).toBeFocused();
    await expect(page.getByRole('button', {name: '重算历史'})).toBeVisible();
    await expect(page.getByText(/胜率 100\.0%/)).toBeVisible();
    await expect(table).toContainText('2026-11-12');
    await expect(table).toHaveClass(/\btrade-table\b/);
    const stockInput = toolbar.getByRole('textbox', {name: '股票', exact: true});
    await expect(toolbar.getByRole('textbox')).toHaveCount(2);
    await expect(toolbar.getByRole('button', {name: '查看K线', exact: true})).toHaveCount(0);
    await expect(toolbar.getByRole('button', {name: '筛选', exact: true})).toBeVisible();
    await stockInput.fill('浦发银行');
    const filtered = page.waitForResponse(response => {
      const url = new URL(response.url());
      return url.pathname === '/api/screen/history' && url.searchParams.get('code') === '浦发银行';
    });
    await stockInput.press('Enter');
    await filtered;
    await expect(toolbar.getByRole('button', {name: '清除股票筛选'})).toBeVisible();
    const historyStock = table.getByRole('button', {name: /查看 sh600000 浦发银行/});
    await historyStock.click();
    await expect(dialog.getByRole('heading')).toContainText('浦发银行');
    await page.keyboard.press('Escape');
    await expect(historyStock).toBeFocused();
    await expect(page.getByRole('region', {name: '策略列表'}).getByRole('textbox')).toHaveCount(0);
    const bounds = await table.boundingBox();
    const signalSpace = await page.locator('.screen-signal-workspace').boundingBox();
    expect(bounds.width).toBeGreaterThan(signalSpace.width - 24);
    expect(bounds.width).toBeLessThanOrEqual(signalSpace.width);
    expect(bounds.height).toBeGreaterThan(width === 1440 ? 500 : 300);
    if (width === 1440) {
      expect(await page.evaluate(() => document.documentElement.scrollHeight)).toBeLessThanOrEqual(900);
    }
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.getByRole('tab', {name: '当日买点'}).click();
    await expect(buy).toBeVisible();
    await expect(historyFilters).toBeHidden();
    expect(errors).toEqual([]);
  });
}

for (const width of [1440, 390]) {
  test(`卖点提醒浮层不移动正文，支持关闭、重开及点击代码查看 K 线（${width}px）`, async ({page}) => {
    await page.setViewportSize({width, height: 844});
    const strategy = {id: 'macd-premium', name: 'MACD精选'};
    const signal = {code: 'sh600000', name: '浦发银行', strategy: strategy.id,
      buyTime: '2026-11-12 15:00:00', buyPrice: 10, sellTime: '2026-11-13 10:01:00', sellPrice: 11,
      tracked: true, sellSource: 'live', rate: 10};
    const message = id => JSON.stringify({type:'sell', id, strategyName:strategy.name, signal});
    let socket;
    await page.routeWebSocket('**/api/screen/events?*', ws => { socket = ws; });
    await page.route('**/api/screen', async route => {
      await new Promise(resolve => setTimeout(resolve, 300));
      return route.fulfill({json:{config:{version:1, strategies:[strategy]}, running:false}});
    });
    await page.route('**/api/screen/history?*', route => route.fulfill({json:{
      rows:[signal], total:1, page:1, pages:1, running:false, lookback:60, avgHoldingDays:1, stats:{total:1, winRate:100, profitFactor:null},
    }}));
    const bars = [
      {date:'2026-11-12',open:10,high:10,low:10,close:10,volume:100,dif:0,dea:0,macd:0},
      {date:'2026-11-13',open:10,high:11,low:10,close:11,volume:120,dif:0,dea:0,macd:0},
    ];
    await page.route('**/api/screen/diagnose/**', route => route.fulfill({json:{
      code:signal.code, name:signal.name, asOf:signal.sellTime, signals:[signal], rules:[], klines:bars,
    }}));
    await page.route('**/api/kline/sh600000?*', route => route.fulfill({json:bars}));
    await page.goto('/?tab=trades');
    await expect.poll(() => Boolean(socket)).toBe(true);
    const heading = page.getByRole('heading', {level: 1});
    await expect(heading).toHaveText('交易明细');
    const content = page.locator('#tab3');
    await expect(content.locator('.tabulator-row').first()).toBeVisible();
    await content.focus();
    const before = await content.boundingBox();
    socket.send(message(1));
    socket.send(message(1));
    const notices = page.getByRole('region', {name:'卖点提醒'});
    const trigger = page.getByRole('button', {name: /^卖点提醒（/, includeHidden: true});
    await expect(notices).toBeVisible();
    await expect(notices.getByRole('listitem')).toHaveCount(1);
    await expect(content).toBeFocused();
    expect(await content.boundingBox()).toEqual(before);
    const bounds = await notices.boundingBox();
    expect(bounds.x).toBeGreaterThanOrEqual(0);
    expect(bounds.x + bounds.width).toBeLessThanOrEqual(width);
    expect(bounds.y + bounds.height).toBeLessThanOrEqual(844);
    await notices.getByRole('button', {name: '关闭'}).click();
    await expect(notices).toBeHidden();
    await expect(trigger).toBeFocused();
    await trigger.press('Enter');
    await expect(notices).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(notices).toBeHidden();
    await expect(trigger).toBeFocused();
    await trigger.press('Enter');
    await notices.getByRole('button', {name: '查看 sh600000 浦发银行 日K线'}).press('Enter');
    const stockChart = page.getByRole('dialog', {name: 'sh600000 浦发银行 · 日K线'});
    await expect(stockChart.locator('canvas').first()).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(notices).toBeHidden();
    await expect(trigger).toBeFocused();
    await expect(page).toHaveURL(/tab=trades$/);
    await trigger.press('Enter');
    await expect(notices.getByRole('button', {name: '查看K线'})).toHaveCount(0);
    await expect(notices.locator('button.stock-chart-button')).toHaveCount(1);
    socket.send(message(2));
    await expect(trigger).toHaveText('卖点提醒（2）');
    await expect(notices.getByRole('listitem')).toHaveCount(2);
    await notices.getByRole('button', {name: '清除提醒'}).click();
    await expect(notices).toBeHidden();
    await expect(trigger).toHaveCount(0);
    await expect(heading).toBeFocused();
  });
}

test('历史只重算所选策略，保留全局进度并滚动查看全部结果', async ({page}) => {
  const other = {id: 'macd-premium', name: 'MACD精选'};
  const strategy = {id: 'macd-base', name: 'MACD基础(测试)'};
  await page.route('**/api/screen', route => route.fulfill({json: {
    config: {version: 1, strategies: [other, strategy]}, running: false,
  }}));
  let runs = 0;
  let attempts = 0;
  let finished = false;
  let rejectRun = true;
  await page.route('**/api/status', route => route.fulfill({json: runs === 0 ? {state: 'idle'} : {
    state: finished ? 'done' : 'running', task: 'screen_history', phase: 'scan', unit: 'stocks',
    done: finished ? 10 : 3, total: 10, currentCode: 'sh600000',
  }}));
  await page.route('**/api/screen/history**', route => {
    const query = new URL(route.request().url()).searchParams;
    if (route.request().method() === 'POST') {
      attempts++;
      expect(query.get('lookback')).toBe('120');
      expect(query.getAll('strategy')).toEqual([strategy.id]);
      if (rejectRun) return route.fulfill({status: 409, json: {error: '已有批量任务在运行'}});
      runs++;
      return route.fulfill({json: {running: true}});
    }
    const history = query.get('mode') === 'history';
    if (history) expect(query.has('page')).toBe(false);
    const selected = query.get('strategy');
    const forSelected = history && selected === strategy.id;
    const rows = history && finished && (!selected || forSelected) ? Array.from({length: 60}, (_, index) => ({
      code: `sh${600000 + index}`,
      name: index === 0 ? '浦发银行' : `测试股票 ${index + 1}`, strategy: strategy.id,
      buyTime: '2026-11-12 15:00:00', sellTime: '2026-11-13 10:00:00', buyPrice: 10, sellPrice: 11, rate: 10,
    })) : [];
    return route.fulfill({json: {rows, total: rows.length, page: 1, pages: 1,
      running: runs > 0 && forSelected && !finished, through: forSelected && finished ? '2026-11-13' : '',
      lookback: forSelected && finished ? 120 : 60, defaultLookback: 60}});
  });
  await page.goto('/?tab=screen');
  await page.getByRole('tab', {name: '历史交易'}).click();
  await expect(page.getByText('当前条件下暂无交易记录。')).toBeVisible();
  expect(runs).toBe(0);
  const rebuild = page.getByRole('button', {name: '重算历史', exact: true});
  await expect(rebuild).toBeDisabled();
  const selectStrategy = name => page.getByRole('region', {name: '策略列表'}).getByRole('button', {name, exact: true}).click();
  await selectStrategy(strategy.name);
  const dialog = page.getByRole('dialog', {name: '重算历史'});
  const lookback = dialog.getByRole('combobox', {name: '回算范围'});
  const start = dialog.getByRole('button', {name: '开始重算'});
  await expect(lookback).toBeHidden();
  await rebuild.click();
  await expect(dialog).toHaveAccessibleDescription('本次仅重算「MACD基础(测试)」的历史交易。');
  await expect(lookback).toBeFocused();
  await lookback.selectOption('120');
  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();
  await expect(rebuild).toBeFocused();
  expect(attempts).toBe(0);
  await rebuild.click();
  await expect(lookback.locator('option:checked')).toHaveText('最近 60 个交易日（默认）');
  await lookback.selectOption('120');
  await start.click();
  await expect(dialog.getByRole('alert')).toHaveText('已有批量任务在运行');
  await expect(lookback).toHaveValue('120');
  rejectRun = false;
  await start.click();
  await expect(dialog).toBeHidden();
  const progress = page.getByRole('progressbar', {name: /历史选股回算/});
  await expect(progress).toHaveAttribute('value', '30');
  await expect(progress).toHaveAttribute('aria-valuetext', /3\/10 只股票/);
  await expect(rebuild).toBeDisabled();
  expect(attempts).toBe(2);
  expect(runs).toBe(1);
  await page.getByRole('tab', {name: '当日卖点'}).click();
  await expect(page.getByText('今日暂无已入库的卖点。')).toBeVisible();
  await expect(progress).toBeVisible();
  await page.getByRole('navigation', {name: '主导航'}).getByRole('button', {name: '盘感练习'}).click();
  await expect(progress).toBeVisible();
  await page.getByRole('navigation', {name: '主导航'}).getByRole('button', {name: '选股策略'}).click();
  await page.getByRole('tab', {name: '历史交易'}).click();
  finished = true;
  await expect(progress).toHaveAttribute('value', '100');
  await expect(page.getByText('上次重算截至 2026-11-13 · 回看 120 个交易日')).toBeVisible();
  await expect(page.locator('.screen-signal-table')).toContainText('浦发银行');
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollHeight - innerHeight)).toBeLessThanOrEqual(0);
  const tableBody = page.locator('.screen-signal-table .tabulator-tableholder');
  await expect.poll(() => tableBody.evaluate(el => el.scrollHeight - el.clientHeight)).toBeGreaterThan(0);
  const lastRowButton = tableBody.getByRole('button', {name: /sh600059/});
  await expect(async () => {
    await tableBody.evaluate(el => { el.scrollTop = el.scrollHeight; });
    await expect(lastRowButton).toBeInViewport({timeout: 500});
  }).toPass({timeout: 5000});
  await expect(page.getByRole('button', {name: '下一页', exact: true})).toHaveCount(0);
  await expect(page.getByRole('button', {name: '上一页', exact: true})).toHaveCount(0);
  await selectStrategy(other.name);
  await expect(page.getByText('上次重算截至 2026-11-13 · 回看 120 个交易日')).toBeHidden();
  await expect(page.getByText('当前条件下暂无交易记录。')).toBeVisible();
  await selectStrategy(strategy.name);
  await page.getByRole('tab', {name: '当日买点'}).click();
  await page.getByRole('tab', {name: '历史交易'}).click();
  await expect(page.locator('.screen-signal-table')).toContainText('浦发银行');
  await page.reload();
  await page.getByRole('tab', {name: '历史交易'}).click();
  await expect(page.locator('.screen-signal-table')).toContainText('浦发银行');
  expect(runs).toBe(1);
  await expect(page).toHaveURL(/tab=screen$/);
});

for (const width of [1440, 390]) {
  test(`${width}px 左侧策略列表可滚动，历史结果按筛选显示统计`, async ({page}) => {
    await page.setViewportSize({width, height: 844});
    const first = {id: 'macd-premium', name: 'MACD精选'};
    const second = {id: 'macd-base', name: 'MACD基础(测试)'};
    const extraStrategies = Array.from({length: 30}, (_, index) => ({
      id: 'test-' + index, name: '测试策略 ' + (index + 1),
    }));
    await page.route('**/api/screen', route => route.fulfill({json: {
      config: {version: 1, strategies: [first, second, ...extraStrategies]}, running: false,
      strategyStats: {[first.id]: {total: 59, closed: 51, marked: 8, winRate: 58.8, profitFactor: 0.68}, [second.id]: {total: 0}},
    }}));
    await page.route('**/api/screen/history?*', route => {
      const filtered = new URL(route.request().url()).searchParams.get('strategy') === second.id;
      const rows = filtered ? [] : [{code: 'sh600000', strategy: first.id, buyTime: '2026-11-12 15:00:00',
        buyPrice: 10, lastPrice: 12, rate: 20, tracked: true}, {code: 'sh600001', strategy: first.id,
        buyTime: '2026-11-12 15:00:00', buyPrice: 10, rate: 0, tracked: true}];
      return route.fulfill({json: {rows, total: rows.length, page: 1, pages: 1, through: '2026-11-13', lookback: 60,
        stats: filtered ? {total: 0} : {total: 59, closed: 51, marked: 8, unpriced: 1, winRate: 58.8, profitFactor: 0.68},
        avgHoldingDays: 16.1}});
    });
    await page.goto('/?tab=screen');
    const library = page.getByRole('region', {name: '策略列表'});
    await expect(library).toBeVisible();
    const list = library.getByRole('list');
    await expect(list.getByRole('button')).toHaveCount(32);
    await expect(list.locator('input')).toHaveCount(0);
    const secondStrategy = list.getByRole('button', {name: second.name, exact: true});
    await expect(list.locator('[aria-current="true"]')).toHaveCount(0);
    const cards = library.getByRole('listitem');
    await expect(cards.nth(0)).toContainText('历史胜率 58.8%');
    await expect(cards.nth(0)).toContainText('盈亏比 0.68');
    await expect(cards.nth(1)).toContainText('历史胜率 —');
    await page.getByRole('tab', {name: '历史交易'}).click();
    const rebuild = page.getByRole('button', {name: '重算历史', exact: true});
    await expect(rebuild).toBeDisabled();
    await list.getByRole('button', {name: first.name, exact: true}).click();
    await rebuild.click();
    const rebuildDialog = page.getByRole('dialog', {name: '重算历史'});
    await expect(rebuildDialog.getByRole('combobox', {name: '回算范围'})).toBeInViewport();
    await expect(rebuildDialog.getByRole('button', {name: '开始重算'})).toBeInViewport();
    await rebuildDialog.getByRole('button', {name: '取消'}).click();
    await expect(rebuild).toBeFocused();
    const summary = page.locator('.screen-history-performance');
    await expect(summary).toContainText('已卖出 51 笔');
    await expect(summary).toContainText('持仓估值 8 笔');
    await expect(summary).toContainText('缺价 1 笔');
    await expect(summary).toContainText('胜率 58.8%');
    await expect(summary).toContainText('盈亏比 0.68');
    await expect(summary).toContainText('平均持有 16.1 天');
    const table = page.locator('.screen-signal-table');
    const priced = table.locator('.tabulator-row').filter({hasText: 'sh600000'});
    const unpriced = table.locator('.tabulator-row').filter({hasText: 'sh600001'});
    await expect(priced.locator('[tabulator-field="lastPrice"]')).toHaveText('12.00');
    await expect(priced.locator('[tabulator-field="rate"]')).toHaveText('20.00');
    await expect(unpriced.locator('[tabulator-field="rate"]')).toHaveText('—');
    const bounds = await summary.boundingBox();
    const panel = await page.getByRole('tabpanel', {name: '历史交易'}).boundingBox();
    const libraryBounds = await library.boundingBox();
    if (width === 390) {
      expect(libraryBounds.y + libraryBounds.height).toBeLessThan(panel.y);
    } else {
      expect(libraryBounds.x + libraryBounds.width).toBeLessThan(panel.x);
    }
    expect(panel.x + panel.width - bounds.x - bounds.width).toBeLessThan(15);
    expect(await list.evaluate(el => el.scrollHeight > el.clientHeight)).toBe(true);
    await list.getByRole('button', {name: '测试策略 30', exact: true}).focus();
    await expect.poll(() => list.evaluate(el => el.scrollTop)).toBeGreaterThan(0);
    const panelAfterScroll = await page.getByRole('tabpanel', {name: '历史交易'}).boundingBox();
    expect(panelAfterScroll.width).toBe(panel.width);
    await secondStrategy.press('Enter');
    await expect(secondStrategy).toHaveAttribute('aria-current', 'true');
    await expect(list.locator('[aria-current="true"]')).toHaveCount(1);
    await expect(list.locator('.is-selected')).toHaveCount(1);
    await expect(summary).toBeHidden();
    await expect(table).toBeHidden();
    await expect(page.getByText('当前条件下暂无交易记录。')).toBeVisible();
    const sellRequest = page.waitForRequest(request => {
      const url = new URL(request.url());
      return url.pathname === '/api/screen/history' && url.searchParams.get('mode') === 'sell' &&
        url.searchParams.get('strategy') === second.id;
    });
    await page.getByRole('tab', {name: '当日卖点'}).click();
    await sellRequest;
    await page.getByRole('tab', {name: '当日买点'}).click();
    await expect(page.getByText('点击「立即选股」扫描已启用的策略。')).toBeVisible();
    await expect(cards.nth(0)).toContainText('历史胜率 58.8%');
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  });
}

test('窄屏下自动和手动选股共用任务栏，手动扫描可停止', async ({page}) => {
  await page.setViewportSize({width: 390, height: 844});
  const config = {version: 1, strategies: [{id: 'macd-premium', name: 'MACD精选'}]};
  let preview = true;
  let started = false;
  let stopped = false;
  await page.route('**/api/screen', route => route.fulfill({json: {config, running: preview || (started && !stopped),
    preview: preview ? {done: 1, total: 4, currentCode: 'sh600000'} : undefined}}));
  await page.route('**/api/screen/run', route => {
    started = true;
    return route.fulfill({json: {config, running: true}});
  });
  await page.route('**/api/status', route => route.fulfill({json: started && !stopped
    ? {state: 'running', task: 'screen_scan', phase: 'scan', unit: 'stocks', done: 2, total: 4}
    : {state: 'idle', task: started ? 'screen_scan' : 'backtest'}}));
  await page.route('**/api/stop', route => {
    stopped = true;
    return route.fulfill({json: {ok: true}});
  });
  await page.goto('/?tab=screen');
  const progress = page.getByRole('progressbar', {name: /选股扫描/});
  const stop = page.getByRole('button', {name: '停止任务'});
  await expect(progress).toHaveAttribute('value', '25');
  await expect(stop).toHaveCount(0);
  preview = false;
  const run = page.getByRole('region', {name: '策略列表'}).getByRole('button', {name: '立即选股'});
  await run.click();
  await expect(progress).toHaveAttribute('value', '50');
  await stop.click();
  await expect(page.getByText('选股扫描已停止')).toBeVisible();
  await expect(run).toBeEnabled();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
