import { test, expect } from '@playwright/test';

test.beforeEach(async ({page}) => {
  await page.route('https://cdn.jsdelivr.net/**', route => route.fulfill({contentType: 'text/javascript', body: ''}));
});

test('配置多套策略、合并命中、按策略查看并保留配置', async ({page}) => {
  await page.goto('/?tab=screen');
  await expect(page.getByRole('heading', {level: 1})).toHaveText('选股策略');
  const library = page.getByRole('button', {name: '策略列表'});
  await library.click();
  await expect(page.getByRole('checkbox', {name: /^MACD精选\s*内置$/})).not.toBeChecked();
  await expect(page.getByRole('button', {name: '立即选股'})).toBeDisabled();

  // Keep the actual service for config writes. The browser fixture has no market
  // attached, so inject two synthetic hits for the results presentation.
  const own = ['价格筛选 A', '价格筛选 B'];
  for (const name of own) {
    await page.getByRole('button', {name: '新建策略'}).click();
    const dlg = page.getByRole('dialog');
    await dlg.getByRole('textbox', {name: '策略名称'}).fill(name);
    const save = dlg.getByRole('button', {name: '保存策略'});
    await save.click();
    await expect(dlg.getByRole('alert')).toHaveText('请添加条件或删除空分组');
    await dlg.getByRole('button', {name: '+ 条件', exact: true}).click();
    const component = dlg.getByRole('combobox', {name: /选择组件/});
    await component.selectOption('buy_price');
    await dlg.getByRole('spinbutton', {name: /最低价/}).fill('5');
    await dlg.getByRole('checkbox', {name: '保存后启用'}).check();
    await save.click();
    await expect(dlg).toBeHidden();
  }

  await page.route('**/api/screen', async route => {
    if (route.request().method() !== 'GET') return route.continue();
    const response = await route.fetch();
    const payload = await response.json();
    const enabled = payload.config.strategies.filter(strategy => strategy.enabled);
    await route.fulfill({response, json: {...payload, running: false, error: '',
      result: {asOf: '2026-11-13 10:00:00', universe: 1, evaluated: 1, noSessionBars: 0, failed: 0,
        matches: enabled.length ? [{code: 'sh600000', name: '浦发银行', price: 12, rise: 1.25, time: '2026-11-13 09:59:00',
          hits: enabled.map(strategy => ({id: strategy.id}))}] : []}}});
  });
  await page.reload();
  await expect(page.getByText('2 套已启用')).toBeVisible();
  await library.click();
  const table = page.getByRole('table', {name: /选股结果/});
  await expect(table.getByRole('row')).toHaveCount(2);
  await expect(table).toContainText('价格筛选 A');
  await expect(table).toContainText('价格筛选 B');
  await expect(table).toContainText('+1.25%');
  const filter = page.getByRole('combobox', {name: '策略', exact: true});
  await filter.focus();
  await filter.press('Space');
  await filter.press('Escape');
  await filter.selectOption({label: '价格筛选 A'});
  await expect(filter.locator('option:checked')).toHaveText('价格筛选 A');
  await expect(table.getByRole('row')).toHaveCount(2);

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
  await expect(page.getByText('1 套已启用')).toBeVisible();
  const checkbox = page.getByRole('checkbox', {name: /^价格筛选 B\s*自定义$/});
  await checkbox.click();
  await expect(checkbox).not.toBeChecked();
  await expect(page.getByText('0 套已启用')).toBeVisible();
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
  const library = page.getByRole('button', {name: '策略列表', exact: true});
  await expect(library).toHaveAttribute('aria-expanded', 'false');
  await library.focus();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('region', {name: '策略列表'})).toBeVisible();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('region', {name: '策略列表'})).toBeHidden();
  await page.getByRole('button', {name: '新建策略'}).click();
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
    const strategy = {id: 'macd-premium', name: 'MACD精选', enabled: true};
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
    const buy = page.getByRole('button', {name: '查看 sh600000 K线', exact: true});
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
    const table = page.locator('.screen-signal-table');
    await expect(table).toHaveClass(/tabulator/);
    await expect(table).toContainText('分钟线');
    await table.getByRole('button', {name: /查看 sh600000 浦发银行/}).click();
    await expect(dialog.locator('.screen-kline canvas').first()).toBeVisible();
    await dialog.getByRole('button', {name: '关闭', exact: true}).click();

    await sell.focus();
    await page.keyboard.press('ArrowRight');
    await expect(page.getByRole('tab', {name: '历史买卖'})).toBeFocused();
    await expect(page.getByText(/胜率 100\.0%/)).toBeVisible();
    await expect(table).toContainText('2026-11-12');
    await expect(table).toHaveClass(/\btrade-table\b/);
    const bounds = await table.boundingBox();
    expect(bounds.width).toBeGreaterThan(width === 1440 ? 1100 : 300);
    expect(bounds.height).toBeGreaterThan(width === 1440 ? 500 : 300);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    expect(errors).toEqual([]);
  });
}

test('其他工作区收到已提交卖点，可直接打开共用诊断且重复消息只显示一次', async ({page}) => {
  const strategy = {id: 'macd-premium', name: 'MACD精选', enabled: true};
  const signal = {code: 'sh600000', name: '浦发银行', strategy: strategy.id,
    buyTime: '2026-11-12 15:00:00', buyPrice: 10, sellTime: '2026-11-13 10:01:00', sellPrice: 11,
    tracked: true, sellSource: 'live', rate: 10};
  const message = JSON.stringify({type:'sell', id:1, strategyName:strategy.name, signal});
  await page.routeWebSocket('**/api/screen/events?*', ws => {
    ws.send(message);
    ws.send(message);
  });
  await page.route('**/api/screen', async route => {
    await new Promise(resolve => setTimeout(resolve, 300));
    return route.fulfill({json:{config:{version:1, strategies:[strategy]}, running:false}});
  });
  await page.route('**/api/screen/history?*', route => route.fulfill({json:{
    rows:[signal], total:1, page:1, pages:1, running:false, lookback:60, avgHoldingDays:1, stats:{total:1, winRate:100, profitFactor:null},
  }}));
  await page.route('**/api/screen/diagnose/**', route => route.fulfill({json:{
    code:signal.code, name:signal.name, asOf:signal.sellTime, signals:[signal], rules:[],
    klines:[{date:'2026-11-12',open:10,high:10,low:10,close:10,volume:100,dif:0,dea:0,macd:0},
      {date:'2026-11-13',open:10,high:11,low:10,close:11,volume:120,dif:0,dea:0,macd:0}],
  }}));
  await page.goto('/?tab=trades');
  const notices = page.getByRole('region', {name:'卖点提醒'});
  await expect(notices.getByRole('listitem')).toHaveCount(1);
  await notices.getByRole('button',{name:'查看K线'}).click();
  await expect(page.getByRole('dialog').locator('.screen-kline canvas').first()).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('tab',{name:'当日卖点'})).toHaveAttribute('aria-selected','true');
  await expect(page.locator('.screen-signal-table')).toContainText('本地交易');
});

test('历史默认读取本地库，手动回算在全局任务栏显示真实进度', async ({page}) => {
  const strategy = {id: 'macd-premium', name: 'MACD精选', enabled: true};
  await page.route('**/api/screen', route => route.fulfill({json: {
    config: {version: 1, strategies: [strategy]}, running: false,
  }}));
  let runs = 0;
  let finished = false;
  await page.route('**/api/status', route => route.fulfill({json: runs === 0 ? {state: 'idle'} : {
    state: finished ? 'done' : 'running', task: 'screen_history', phase: 'scan', unit: 'stocks',
    done: finished ? 10 : 3, total: 10, currentCode: 'sh600000',
  }}));
  await page.route('**/api/screen/history**', route => {
    if (route.request().method() === 'POST') {
      runs++;
      return route.fulfill({json: {running: true}});
    }
    const query = new URL(route.request().url()).searchParams;
    const history = query.get('mode') === 'history';
    const rows = history && finished ? [{code: 'sh600000', name: '浦发银行', strategy: strategy.id,
      buyTime: '2026-11-12 15:00:00', sellTime: '2026-11-13 10:00:00', buyPrice: 10, sellPrice: 11, rate: 10}] : [];
    return route.fulfill({json: {rows, total: rows.length, page: 1, pages: 1,
      running: runs > 0 && history && !finished, through: history && finished ? '2026-11-13' : '', lookback: 60}});
  });
  await page.goto('/?tab=screen');
  await page.getByRole('tab', {name: '历史买卖'}).click();
  await expect(page.getByText('当前条件下暂无交易记录。')).toBeVisible();
  expect(runs).toBe(0);
  await page.getByRole('button', {name: '重算历史'}).click();
  const progress = page.getByRole('progressbar', {name: /历史选股回算/});
  await expect(progress).toHaveAttribute('value', '30');
  await expect(progress).toHaveAttribute('aria-valuetext', /3\/10 只股票/);
  expect(runs).toBe(1);
  await page.getByRole('tab', {name: '当日卖点'}).click();
  await expect(page.getByText('今日暂无已入库的卖点。')).toBeVisible();
  await expect(progress).toBeVisible();
  await page.getByRole('navigation', {name: '主导航'}).getByRole('button', {name: '盘感练习'}).click();
  await expect(progress).toBeVisible();
  await page.getByRole('navigation', {name: '主导航'}).getByRole('button', {name: '选股策略'}).click();
  await page.getByRole('tab', {name: '历史买卖'}).click();
  finished = true;
  await expect(progress).toHaveAttribute('value', '100');
  await expect(page.getByText('上次重算截至 2026-11-13', {exact: false})).toBeVisible();
  await expect(page.locator('.screen-signal-table')).toContainText('浦发银行');
  await page.getByRole('tab', {name: '当日买点'}).click();
  await page.getByRole('tab', {name: '历史买卖'}).click();
  await expect(page.locator('.screen-signal-table')).toContainText('浦发银行');
  await page.reload();
  await page.getByRole('tab', {name: '历史买卖'}).click();
  await expect(page.locator('.screen-signal-table')).toContainText('浦发银行');
  expect(runs).toBe(1);
  await expect(page).toHaveURL(/tab=screen$/);
});

test('窄屏下自动和手动选股共用任务栏，手动扫描可停止', async ({page}) => {
  await page.setViewportSize({width: 390, height: 844});
  const config = {version: 1, strategies: [{id: 'macd-premium', name: 'MACD精选', enabled: true}]};
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
  await page.getByRole('button', {name: '立即选股'}).click();
  await expect(progress).toHaveAttribute('value', '50');
  await stop.click();
  await expect(page.getByText('选股扫描已停止')).toBeVisible();
  await expect(page.getByRole('button', {name: '立即选股'})).toBeEnabled();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
