import {test, expect} from '@playwright/test';

const codes = ['sh601398', 'sh601939', 'sh601288', 'sh601988', 'sh601328', 'sh601166',
  'sh601088', 'sh601225', 'sh600188', 'sh601898', 'sh600900', 'sh600886', 'sh601006',
  'sh600028', 'sh601857', 'sh600019', 'sh601668', 'sz000895', 'sh600941', 'sh601728'];
const dialog = page => page.getByRole('dialog', {name: '运行回测', exact: true});
const sample = page => dialog(page).getByRole('combobox', {name: '股票范围'});

async function openBuilder(page) {
  await page.route('https://cdn.jsdelivr.net/**', route => route.fulfill({contentType: 'text/javascript', body: ''}));
  await page.goto('/?tab=strategy');
  const run = page.locator('.builder-editor').getByRole('button', {name: '运行回测', exact: true});
  await expect(run).toBeEnabled();
  // Only the draft is synthetic; exercise the real dialog, payload builder, and API client.
  await page.evaluate(async () => {
    const {state} = await import('/js/store.js');
    state.builderSpec = {version: 1, name: '红利样本测试',
      buy: {type: 'group', op: 'and', children: [{type: 'condition', componentId: 'buy_all', params: {}}]},
      sellSignal: null, holdToEnd: true, risks: []};
  });
  await run.click();
  await expect(dialog(page)).toBeVisible();
}

for (const width of [1440, 390]) {
  test(`${width}px 红利20只提交完整代码，失败保留选择并能重试`, async ({page}) => {
    await page.setViewportSize({width, height: 1000});
    const errors = []; page.on('pageerror', e => errors.push(e.message));
    const payloads = [];
    await page.route('**/api/builder/run', route => {
      payloads.push(route.request().postDataJSON());
      return payloads.length === 1
        ? route.fulfill({status: 503, json: {error: '测试服务暂不可用'}})
        : route.fulfill({json: {}});
    });
    await openBuilder(page);
    await sample(page).selectOption({label: '红利参考股（20只）'});
    await expect(dialog(page).getByLabel('股票代码', {exact: true})).toHaveValue(codes.join(','));
    await expect(dialog(page)).toContainText('人工参考名单');
    await expect(sample(page).locator('optgroup option')).toHaveCount(21);
    await expect(sample(page).getByRole('option', {name: '601166 兴业银行', exact: true})).toHaveCount(1);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    expect(await dialog(page).evaluate(el => {
      const rect = el.getBoundingClientRect(); return rect.left >= 0 && rect.right <= innerWidth;
    })).toBe(true);
    await page.screenshot({path: `output/browser-tests/dividend-options-${width}.png`, fullPage: true});
    await dialog(page).getByRole('button', {name: '开始回测', exact: true}).click();
    await expect(dialog(page).getByRole('alert')).toContainText('测试服务暂不可用');
    await expect(sample(page)).toHaveValue('dividend');
    expect(payloads[0].sampleMode).toBe('codes');
    expect(payloads[0].sampleCodes).toEqual(codes);
    await dialog(page).getByRole('button', {name: '开始回测', exact: true}).click();
    await expect(dialog(page)).not.toBeVisible();
    expect(payloads[1].sampleCodes).toEqual(codes);
    expect(errors).toEqual([]);
  });

  test(`${width}px 单股键盘选择、编辑代码和取消不提交`, async ({page}) => {
    await page.setViewportSize({width, height: 1000});
    let calls = 0, payload;
    await page.route('**/api/builder/run', route => {
      calls++; payload = route.request().postDataJSON();
      return route.fulfill({status: 503, json: {error: '保留配置供检查'}});
    });
    await openBuilder(page);
    await sample(page).selectOption('dividend');
    // Native select typeahead works with the visible stock code on macOS too.
    await sample(page).focus();
    await page.keyboard.type('601398', {delay: 30});
    await page.keyboard.press('Tab');
    await expect(sample(page)).toHaveValue('sh601398');
    await expect(dialog(page).getByLabel('股票代码', {exact: true})).toHaveValue('sh601398');
    await sample(page).selectOption({label: '601166 兴业银行'});
    await dialog(page).getByRole('button', {name: '开始回测', exact: true}).click();
    await expect(dialog(page).getByRole('alert')).toContainText('保留配置供检查');
    expect(payload.sampleMode).toBe('codes'); expect(payload.sampleCodes).toEqual(['sh601166']);
    await dialog(page).getByLabel('股票代码', {exact: true}).fill('sh601166,sh600188');
    await expect(sample(page)).toHaveValue('codes');
    await page.keyboard.press('Escape');
    await expect(dialog(page)).not.toBeVisible();
    const stored = await page.evaluate(async () => (await import('/js/store.js')).state.runCfg.sampleCodes);
    expect(stored).toBe('sh601166'); expect(calls).toBe(1);
    await expect(page.locator('.builder-editor').getByRole('button', {name: '运行回测', exact: true})).toBeFocused();
  });
}
