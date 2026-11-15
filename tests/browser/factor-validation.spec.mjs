import { expect, test } from '@playwright/test';

for (const width of [1440, 390]) {
  test(`${width}px 候选冻结验证后可在组合模型中找到通过的因子`, async ({page}) => {
    await page.setViewportSize({width, height: 900});
    await page.route('https://cdn.jsdelivr.net/**', route => route.fulfill({contentType: 'text/javascript', body: ''}));
    const candidate = {
      id: 'fc_20261114T150100000Z_99aabbcc', name: '动量候选', revision: 1, status: 'candidate',
      factor: {kind: 'momentum', name: '动量', days: 20, implementationVersion: 1}, use: {mode: 'observe'},
    };
    const id = 'fv_20261114T150100000Z_99aabbcc';
    const requests = [];
    let phase = '';
    const reference = {validationId: id, factorKind: 'momentum', factorDays: 20, direction: 'higher_is_better',
      candidateRevision: 1, implementationVersion: 1, primaryHorizon: 1, evidenceClass: 'retrospective'};
    const validation = () => ({request: {id, protocol: {testRange: {start: '2025-01-01', end: '2025-06-30'}}},
      state: phase, ...(phase === 'passed' && {windows: [{index: 1, startDate: '2025-01-01', endDate: '2025-06-30',
        observations: 100, horizons: [{horizon: 1, ic: 0.03, coverage: 0.97, spread: .01, topExcess: .004}]}],
      report: {checks: [{name: '有效窗口', actual: '1', expected: '1'}]}})});
    await page.route(/\/api\/factor-candidates$/, route => route.fulfill({json: {candidates: [candidate]}}));
    await page.route('**/validation-inputs', route => route.fulfill({json: {
      candidateRevision: 1, analysisId: 'an_20241114T150100000Z_99aabbcc', trialIds: ['tr_20241114T150100000Z_99aabbcc'],
      purgeDays: 2, direction: 'positive', lastSignalDate: '2024-12-31', allowsRetrospective: true,
    }}));
    await page.route(/\/api\/factor-validations(?:\?.*)?$/, async route => {
      if (route.request().method() === 'POST') {
        requests.push(route.request().postDataJSON());
        phase = 'frozen';
        return route.fulfill({status: 201, json: {validation: {id}}});
      }
      return route.fulfill({json: {validations: [], validatedFactors: phase === 'passed' ? [reference] : []}});
    });
    await page.route(`**/api/factor-validations/${id}`, route => route.fulfill({json: validation()}));
    await page.route(`**/api/factor-validations/${id}/run`, route => {
      phase = 'passed';
      return route.fulfill({status: 202, json: {id}});
    });
    await page.route('**/api/status', route => route.fulfill({
      json: phase === 'passed' ? {state: 'done', task: 'factor_validation', runId: id} : {state: 'idle'},
    }));
    await page.goto('/?tab=candidates');
    const openButton = page.getByRole('button', {name: '验证', exact: true});
    await openButton.click();
    const dialog = page.getByRole('dialog', {name: '单因子验证'});
    await dialog.getByLabel('方式').selectOption('retrospective');
    await dialog.getByRole('button', {name: '冻结验证'}).click();
    await expect(dialog.getByRole('alert')).toContainText('测试开始日');
    await expect(dialog.getByLabel('测试开始')).toBeFocused();
    await dialog.getByLabel('测试开始').fill('2025-01-01');
    await dialog.getByLabel('测试结束').fill('2025-06-30');
    expect(await dialog.evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true);
    await dialog.getByRole('button', {name: '冻结验证'}).click();
    expect(requests).toHaveLength(1);
    expect(requests[0]).toMatchObject({candidateRevision: 1, direction: 'positive',
      testRange: {start: '2025-01-01', end: '2025-06-30'}, windows: {purgeDays: 2}});
    await dialog.getByRole('button', {name: '运行验证'}).click();
    await expect(dialog.getByText('有效窗口 1（门槛 1）')).toBeVisible();
    await dialog.getByRole('button', {name: '关闭'}).click();
    await expect(openButton).toBeFocused();
    await page.goto('/?tab=portfolio');
    await page.getByRole('button', {name: /创建模型/}).click();
    await expect(page.getByRole('checkbox', {name: new RegExp(id)})).toBeVisible();
  });
}
