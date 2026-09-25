// Isolated real-extension test host. The parent sends a pairing link over stdin,
// never arguments or logs. Requires a test Chromium and playwright-core.
import { createInterface } from 'node:readline';
import { mkdtemp, rm } from 'node:fs/promises';
import { pathToFileURL } from 'node:url';

const lines = createInterface({ input: process.stdin });
const first = await new Promise(resolve => lines.once('line', resolve));
const { pairing, extension, chromium: executablePath, playwright, fixture } = JSON.parse(first);
const { chromium } = await import(pathToFileURL(playwright).href);
const profile = await mkdtemp('/tmp/wkb-chrome-');
let browser;
let closing = false;
async function close() {
  if (closing) return;
  closing = true;
  await browser?.close();
  await rm(profile, { recursive: true, force: true });
  lines.close();
}
process.on('SIGTERM', () => void close().finally(() => process.exit(0)));
try {
  browser = await chromium.launchPersistentContext(profile, {
    executablePath, headless: process.env.BROWSERSKILL_TEST_HEADED !== '1',
    args: [`--disable-extensions-except=${extension}`, `--load-extension=${extension}`],
  });
  const worker = browser.serviceWorkers()[0] ?? await browser.waitForEvent('serviceworker');
  const popup = await browser.newPage();
  await popup.goto(new URL('popup.html', worker.url()).href);
  await popup.locator('details summary').click();
  // The extension popup unifies local/remote connection settings.
  await popup.locator('[role="group"] button').nth(1).click();
  await popup.locator('#remote-pairing').fill(pairing);
  await popup.locator('form button[type="submit"]').click();
  await popup.waitForFunction(() => document.querySelector('#remote-pairing')?.value === '' || document.querySelector('form [role=alert]'));
  if (await popup.locator('form [role=alert]').count()) throw new Error('Extension authorization failed before browser tests');
  // Official borrow confirmation needs an injectable page in a normal user
  // window. Extension settings and an isolated popup cannot host that prompt.
  const confirmationPage = await browser.newPage();
  await confirmationPage.goto(fixture);
  await confirmationPage.locator('browser-skill-overlay').waitFor({state:'attached'});
  const initial = await worker.evaluate(async () => {
    const window = await chrome.windows.getLastFocused();
    const tabs = await chrome.tabs.query({windowId:window.id,active:true});
    return {windowId:window.id,tabId:tabs[0].id,tabIds:(await chrome.tabs.query({})).map(t=>t.id)};
  });
  const originalWindow = { windowId: initial.windowId, tabId: initial.tabId };
  const preservedTabs = new Set();
  let beforeReturn;
  process.stdout.write('ready\n');
  for await (const command of lines) {
    if (command === 'close') break;
    if (command === 'complete-login' || command === 'interrupt-window' || command === 'approve-borrow') {
      const selector = command === 'complete-login'
        ? '[data-slot="help-request-banner"][data-display-mode="full"] [data-slot="help-continue-button"]'
        : command === 'approve-borrow' ? '[data-slot="borrow-confirmation-allow-button"]'
        : '[data-slot="control-overlay-stop-all"]';
      // Click the actual extension overlay in an isolated fixture browser.
      let clicked = false;
      const deadline = Date.now() + 5000;
      while (!clicked && Date.now() < deadline) {
        for (const page of browser.pages().filter(page => page !== popup).reverse()) {
          const button = command === 'complete-login'
            ? page.locator('[data-slot="help-request-banner"][data-display-mode="full"]')
                .filter({hasText:'Confirm this fixture step'}).locator('[data-slot="help-continue-button"]').first()
            : page.locator(selector).first();
          if (!await button.isVisible()) continue;
          if (command === 'complete-login') {
            // Only synthetic fixture credentials, entered through the page UI.
            await page.locator('#login-account').fill('fixture-user');
            await page.locator('#login-password').fill('fixture-password');
            await page.locator('#login-submit').click();
            await page.waitForURL('**/login-complete');
            await button.waitFor({state:'visible',timeout:5000});
          }
          await button.click({timeout:5000});
          clicked = true;
          break;
        }
        if (!clicked) await new Promise(resolve => setTimeout(resolve, 50));
      }
      if (!clicked) throw new Error('Browser task overlay was not clickable');
      process.stdout.write(command+'-done\n');
    }
    if (command === 'check-detached') {
      // getTargets().attached includes Playwright's own debugger. Probe only
      // this extension's attachment without attaching or changing the page.
      const attached = await worker.evaluate(async () => {
        const tabs = await chrome.tabs.query({});
        const used = [];
        for (const tab of tabs) {
          try {
            await chrome.debugger.sendCommand({tabId:tab.id}, 'Runtime.evaluate', {expression:'0', returnByValue:true});
            used.push(tab.id);
          } catch (error) {
            if (!/not attached|No tab|No target|Cannot access a chrome:\/\/ URL/i.test(String(error))) throw error;
          }
        }
        return used;
      });
      process.stdout.write(JSON.stringify({detached:attached.length===0})+'\n');
    }
    if (command === 'check-cleanup') {
      const state = await worker.evaluate(async () => ({ids:(await chrome.tabs.query({})).map(t=>t.id)}));
      const expected = new Set([...initial.tabIds, ...preservedTabs]);
      const missing = [...expected].filter(id => !state.ids.includes(id));
      const unexpected = state.ids.filter(id => !expected.has(id));
      const cleaned = missing.length === 0 && unexpected.length === 0;
      process.stdout.write(JSON.stringify(cleaned ? {cleaned} : {cleaned, missing, unexpected})+'\n');
    }
    if (command.startsWith('before-tab-return ')) {
      const id = Number(command.split(' ')[1]);
      beforeReturn = await worker.evaluate(async (tabId) => ({
        tabId, tabs: await chrome.tabs.query({}), windows: await chrome.windows.getAll({}),
      }), id);
      process.stdout.write('return-recorded\n');
    }
    if (command.startsWith('expect-returned-tab ')) {
      // Admit only the returned page and the official fallback window's new-tab
      // placeholder. Never allow arbitrary extra tabs to hide task cleanup leaks.
      const returned = JSON.parse(command.slice('expect-returned-tab '.length));
      if (!beforeReturn || beforeReturn.tabId !== returned.tab_id) throw new Error('Missing return snapshot');
      const tabs = await worker.evaluate(async () => chrome.tabs.query({}));
      const target = tabs.find(tab => tab.id === returned.tab_id);
      if (!target || target.windowId !== returned.returned_to_window_id) throw new Error('Returned tab missing or misplaced');
      const added = tabs.filter(tab => !beforeReturn.tabs.some(previous => previous.id === tab.id));
      const newWindow = !beforeReturn.windows.some(window => window.id === target.windowId);
      if (added.length > 1 || added.some(tab => !returned.fallback || !newWindow ||
          tab.windowId !== target.windowId || (tab.pendingUrl ?? tab.url) !== 'chrome://newtab/')) {
        throw new Error('Unexpected tabs created during tab_return');
      }
      preservedTabs.add(target.id);
      for (const tab of added) preservedTabs.add(tab.id);
      beforeReturn = undefined;
      process.stdout.write('returned-tab-preserved\n');
    }
    if (command === 'remember-foreground') {
      const foreground = await worker.evaluate(async () => {
        const window = await chrome.windows.getLastFocused();
        const [tab] = await chrome.tabs.query({windowId:window.id,active:true});
        return {windowId:window.id,tabId:tab.id};
      });
      Object.assign(initial, foreground);
      process.stdout.write('remembered\n');
    }
    if (command === 'create-unowned-tab') {
      const createdPage = browser.waitForEvent('page', {predicate:p => p !== popup});
      const tab = await worker.evaluate(async (initialIds) => {
        const source = (await chrome.tabs.query({})).find(t => !initialIds.includes(t.id) && t.url?.startsWith('http://127.0.0.1:'));
        if (!source) throw new Error('Fixture task page unavailable');
        // Extension API creation simulates a user-created tab: no navigation
        // source event, even though it is inside the Agent Window.
        return chrome.tabs.create({windowId:source.windowId,url:source.url+'?user-tab=1',active:true});
      }, [...initial.tabIds, ...preservedTabs]);
      const page = await createdPage;
      await page.waitForURL('**/*?user-tab=1');
      await page.locator('browser-skill-overlay').waitFor({state:'attached'});
      process.stdout.write(JSON.stringify({tab_id:tab.id})+'\n');
    }
    if (command.startsWith('close-fixture-window ')) {
      const id = Number(command.split(' ')[1]);
      await worker.evaluate(async (tabId) => {
        const tab = await chrome.tabs.get(tabId);
        await chrome.windows.remove(tab.windowId);
      }, id);
      process.stdout.write('fixture-window-closed\n');
    }
    if (command.startsWith('move-fixture-tab-out ')) {
      // Upstream refuses to borrow an unowned tab that already lives in the
      // Agent Window; the user must move it to a regular window first.
      const id = Number(command.split(' ')[1]);
      await worker.evaluate(async ({tabId, windowId}) => {
        await chrome.tabs.move(tabId, {windowId, index:-1});
      }, {tabId:id, windowId:originalWindow.windowId});
      process.stdout.write('fixture-tab-moved\n');
    }
    if (command.startsWith('remove-fixture-tab ')) {
      const id = Number(command.split(' ')[1]);
      await worker.evaluate(async (tabId) => chrome.tabs.remove(tabId), id);
      preservedTabs.delete(id);
      process.stdout.write('fixture-tab-removed\n');
    }
    if (command === 'check-background') {
      const valid = await worker.evaluate(async (original) => {
        const tabs = await chrome.tabs.query({});
        const taskWindows = [...new Set(tabs.filter(t => t.windowId !== original.windowId).map(t => t.windowId))];
        return tabs.find(t => t.id === original.tabId)?.active === true &&
          taskWindows.length > 0 && taskWindows.every(id =>
            tabs.filter(t => t.windowId === id && t.active).length === 1);
      }, originalWindow);
      process.stdout.write(JSON.stringify({background:valid})+'\n');
    }
  }
} finally {
  await close();
}
