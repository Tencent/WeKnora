import assert from 'node:assert/strict';
import { chromium } from 'playwright-core';

const origin = process.env.SITE_TEST_URL || 'http://127.0.0.1:3000';
const key = 'vitepress-theme-appearance';
const browser = await chromium.launch({ channel: 'chrome', headless: true });
const context = await browser.newContext({ colorScheme: 'light', viewport: { width: 1440, height: 1000 } });
const page = await context.newPage();
const errors = [];
context.on('page', p => p.on('pageerror', error => errors.push(error.message)));
page.on('pageerror', error => errors.push(error.message));
async function expectTheme(p, dark) {
  await p.waitForFunction(expected => document.documentElement.classList.contains('dark') === expected, dark);
}
async function homepage(p = page) {
  await p.waitForSelector('#hero-title');
  assert.equal(new URL(p.url()).pathname, '/');
}
async function checkVisibleControl(p, selector) {
  await p.waitForFunction(selector => {
    const box = document.querySelector(selector)?.getBoundingClientRect();
    return box && box.width > 0 && box.x >= 0 && box.right <= innerWidth + 1;
  }, selector);
  const box = await p.locator(selector).boundingBox();
  assert.ok(box && box.width > 0 && box.x >= 0 && box.x + box.width <= p.viewportSize().width + 1, `${selector} must fit in the viewport`);
}
async function headerGeometry(p) {
  return p.locator('.wk-header').evaluate(header => ({
    links: [...header.querySelectorAll('#main-navigation a')].map(a => [a.textContent.trim(), a.getAttribute('href')]),
    controls: [...header.querySelectorAll('.wk-header-inner, .wk-brand, .wk-theme-toggle, .wk-header-github')].map(el => {
      const box = el.getBoundingClientRect();
      return [Math.round(box.x), Math.round(box.y), Math.round(box.width), Math.round(box.height)];
    }),
  }));
}
async function currentSidebarLinkVisible(p) {
  await p.waitForFunction(() => {
    const sidebar = document.querySelector('.VPSidebar');
    const active = sidebar?.querySelector('.VPSidebarItem.is-active > .item > a');
    if (!active) return false;
    const bounds = sidebar.getBoundingClientRect();
    const item = active.getBoundingClientRect();
    return item.height > 0 && item.top >= bounds.top && item.bottom <= bounds.bottom;
  });
}
try {
  assert.equal((await page.goto(origin + '/')).status(), 200);
  await expectTheme(page, false);
  const masthead = await headerGeometry(page);
  await page.getByRole('switch', { name: '切换到深色' }).click();
  await expectTheme(page, true);
  assert.equal(await page.evaluate(k => localStorage.getItem(k), key), 'dark');
  await page.reload();
  await expectTheme(page, true);
  await page.locator('#main-navigation').getByRole('link', { name: '文档', exact: true }).click();
  await page.waitForSelector('.VPDoc h1');
  await expectTheme(page, true);
  assert.equal(new URL(page.url()).pathname, '/docs/01-getting-started/03-quickstart.html');
  assert.match(await page.locator('.VPDoc h1').innerText(), /快速上手/);
  assert.equal(await page.getByRole('link', { name: '文档首页', exact: true }).count(), 0);
  assert.deepEqual((await headerGeometry(page)).controls, masthead.controls);
  assert.deepEqual((await headerGeometry(page)).links.map(link => link[0]), ['快速开始', '架构', '功能', 'API', '客户端', '开发', '版本', 'GitHub']);
  assert.equal(await page.locator('.wk-header').evaluate(el => el.getBoundingClientRect().height), 64);
  assert.equal(context.pages().length, 1);
  // Switching distant sections must reveal the selected sidebar item in both directions.
  for (const section of ['客户端', '开发', '快速开始', '功能', '架构']) {
    await page.locator('#main-navigation').getByRole('link', { name: section, exact: true }).click();
    await page.waitForFunction(section => document.querySelector('#main-navigation a[aria-current="page"]')?.textContent.trim() === section, section);
    await currentSidebarLinkVisible(page);
    assert.ok(await page.evaluate(() => window.scrollY < 2), 'Sidebar synchronization must not scroll the document');
  }
  // Manual sidebar scrolling must remain untouched until navigation occurs.
  await page.locator('.VPSidebar').evaluate(el => el.scrollTop = el.scrollHeight);
  await page.locator('.wk-theme-toggle').click();
  assert.ok(await page.locator('.VPSidebar').evaluate(el => el.scrollTop > 500));
  await page.locator('.wk-theme-toggle').click();
  await page.locator('#main-navigation').getByRole('link', { name: 'API', exact: true }).click();
  await page.waitForFunction(() => location.pathname.includes('/04-api/'));
  await page.waitForSelector('.VPDoc h1');
  await page.waitForFunction(() => document.querySelector('#main-navigation a[aria-current="page"]')?.textContent.trim() === 'API');
  const palette = await page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue('--wk-paper').trim());
  assert.equal(palette, '#0b1220');
  await page.locator('.wk-header-search button').click();
  await page.locator('#localsearch-input').fill('长期记忆');
  await page.waitForSelector('.VPLocalSearchBox .result');
  await page.keyboard.press('Escape');
  await page.locator('.wk-theme-toggle').click();
  await expectTheme(page, false);
  await page.locator('.wk-brand').click();
  await homepage();
  await expectTheme(page, false);
  assert.equal(context.pages().length, 1);

  // Navigation to new v0.8.2 docs, returning through the explicit home link.
  await page.locator('#release').getByRole('link', { name: '了解更多', exact: true }).last().click();
  await page.waitForSelector('.VPDoc h1');
  assert.equal(new URL(page.url()).pathname, '/docs/03-features/18-chat-experience.html');
  assert.match(await page.locator('.VPDoc h1').innerText(), /会话与对话体验/);
  await page.locator('.wk-brand').click();
  await homepage();
  assert.equal(context.pages().length, 1);

  // Preferences propagate between open homepage and docs tabs in both directions.
  const docs = await context.newPage();
  await docs.goto(origin + '/docs/');
  await docs.waitForSelector('.VPDoc h1');
  await docs.locator('.wk-theme-toggle').click();
  await expectTheme(docs, true);
  await expectTheme(page, true);
  await page.getByRole('switch', { name: '切换到浅色' }).click();
  await expectTheme(page, false);
  await expectTheme(docs, false);
  await docs.close();

  // With no explicit selection, follow the operating-system preference.
  const automatic = await browser.newContext({ colorScheme: 'dark' });
  const autoPage = await automatic.newPage();
  await autoPage.goto(origin + '/');
  await expectTheme(autoPage, true);
  await autoPage.emulateMedia({ colorScheme: 'light' });
  await expectTheme(autoPage, false);
  await autoPage.goto(origin + '/docs/');
  await autoPage.waitForSelector('.VPDoc h1');
  await expectTheme(autoPage, false);
  await autoPage.emulateMedia({ colorScheme: 'dark' });
  await expectTheme(autoPage, true);
  await automatic.close();

  // Switch and return links remain reachable on a narrow phone viewport.
  await page.setViewportSize({ width: 320, height: 800 });
  await page.goto(origin + '/');
  await checkVisibleControl(page, 'button[role="switch"]');
  await page.getByRole('switch', { name: '切换到深色' }).click();
  await expectTheme(page, true);
  await page.getByRole('button', { name: '打开导航' }).click();
  await page.locator('#main-navigation').getByRole('link', { name: '文档', exact: true }).click();
  await page.waitForSelector('.VPDoc h1');
  await expectTheme(page, true);
  await checkVisibleControl(page, '.wk-brand');
  await page.locator('.wk-theme-toggle').click();
  await page.getByRole('button', { name: '打开导航' }).click();
  await checkVisibleControl(page, '.wk-header-search button');
  await page.locator('.wk-header-search button').click();
  await page.locator('#localsearch-input').fill('长期记忆');
  await page.waitForSelector('.VPLocalSearchBox .result');
  await page.keyboard.press('Escape');
  await page.keyboard.press('Escape');
  await page.locator('.VPLocalNav button.menu').click();
  await checkVisibleControl(page, '.VPSidebar.open');
  await page.keyboard.press('Escape');
  // A deep link must reveal its entry after hydration, including the mobile drawer.
  await page.goto(origin + '/docs/05-clients/01-frontend.html');
  await page.locator('.VPLocalNav button.menu').click();
  await currentSidebarLinkVisible(page);
  await page.keyboard.press('Escape');
  await expectTheme(page, false);
  await page.locator('.wk-brand').click();
  await homepage();
  await expectTheme(page, false);
  assert.equal(context.pages().length, 1);

  for (const path of ['/docs/03-features/22-skills-sandbox', '/docs/03-features/22-skills-sandbox.html', '/docs/03-features/23-memory.html']) {
    const response = await context.request.get(origin + path);
    assert.equal(response.status(), 200, path);
    assert.ok(!(await response.text()).includes('id="hero-title"'), 'A missing docs route must not fall back to the homepage');
  }
  assert.equal((await context.request.get(origin + '/docs/this-page-does-not-exist')).status(), 404);
  for (const width of [320, 801, 1024, 1920]) {
    await page.setViewportSize({ width, height: 900 });
    for (const path of ['/', '/docs/01-getting-started/02-installation.html']) {
      await page.goto(origin + path);
      await page.waitForSelector('.wk-header');
      const dimensions = await page.locator('.wk-header').evaluate(el => ({ width: el.getBoundingClientRect().width, height: el.getBoundingClientRect().height, scrollWidth: el.scrollWidth }));
      assert.equal(dimensions.height, 64);
      assert.equal(dimensions.width, width);
      assert.equal(dimensions.scrollWidth, width);
      await checkVisibleControl(page, '.wk-brand');
      await page.waitForFunction(() => {
        const image = document.querySelector('.wk-brand img');
        return image?.complete && image.naturalWidth === 945 && image.naturalHeight === 650;
      });
      assert.equal(await page.locator('.wk-brand img').getAttribute('src'), '/docs/_home/brand/weknora-original.png');
      await checkVisibleControl(page, '.wk-theme-toggle');
    }
  }
  assert.deepEqual(errors, []);
  console.log('Integration checks passed: compact full-width mastheads, distinct product/docs navigation, quickstart entry, header search, theme toggle, reload, shared preferences, system theme, same-tab docs/home/logo navigation, mobile controls, and direct docs/404 routes.');
} finally {
  await browser.close();
}
