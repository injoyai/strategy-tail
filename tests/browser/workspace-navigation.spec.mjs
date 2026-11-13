import { expect, test } from '@playwright/test';

for (const width of [1440, 390]) {
  test(`${width}px 因子研究直达因子分析，候选因子保持在同一菜单组`, async ({page}) => {
    await page.setViewportSize({width, height: 900});
    await page.route('https://cdn.jsdelivr.net/**', route => route.fulfill({contentType: 'text/javascript', body: ''}));
    await page.goto('/?tab=candidates');
    await expect(page.getByRole('heading', {level: 1})).toHaveText('候选因子');

    const menu = page.getByRole('button', {name: '菜单', exact: true});
    if (width === 390) await menu.click();
    const research = page.getByRole('button', {name: '因子研究', exact: true});
    const backtest = page.getByRole('button', {name: '策略回测', exact: true});
    const researchGroup = page.locator('.workspace-group').filter({has: research});
    const bounds = await research.boundingBox();

    await backtest.click();
    await expect(researchGroup.getByRole('button', {name: '候选因子'})).toBeVisible();
    expect(await research.boundingBox()).toEqual(bounds);
    await research.click();
    await expect(page).toHaveURL(/\?tab=factor$/);
    await expect(page.getByRole('heading', {level: 1})).toHaveText('因子研究');
    const analysis = researchGroup.getByRole('tab', {name: '因子分析'});
    await expect(analysis).toHaveAttribute('aria-selected', 'true');
    await analysis.focus();
    await page.keyboard.press('ArrowDown');
    const candidate = researchGroup.getByRole('tab', {name: '候选因子'});
    await expect(candidate).toBeFocused();
    await expect(page).toHaveURL(/\?tab=candidates$/);
    await research.click();
    await expect(analysis).toHaveAttribute('aria-selected', 'true');

    await candidate.click();
    await expect(page.getByRole('heading', {level: 1})).toHaveText('候选因子');
    if (width === 390) await expect(menu).toBeFocused();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  });
}
