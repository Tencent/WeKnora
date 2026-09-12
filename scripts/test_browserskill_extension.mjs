// Isolated real-extension test host. The parent sends a pairing link over stdin,
// never arguments or logs. Requires a test Chromium and playwright-core.
import { createInterface } from 'node:readline';
import { mkdtemp, rm } from 'node:fs/promises';
import { pathToFileURL } from 'node:url';

const lines = createInterface({ input: process.stdin });
const first = await new Promise(resolve => lines.once('line', resolve));
const { pairing, extension, chromium: executablePath, playwright } = JSON.parse(first);
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
  const taskWindow = process.env.BROWSERSKILL_TEST_TASK_WINDOW !== '0';
  const popup = await browser.newPage();
  await popup.goto(new URL('popup.html', worker.url()).href);
  await popup.locator('details summary').click();
  await popup.locator('#remote-pairing').fill(pairing);
  await popup.locator('details button').first().click();
  try {
    await popup.waitForFunction(() => document.querySelector('#remote-pairing')?.value === '' || document.querySelector('details [role=alert]'));
  } catch (error) {
    // Never include the password input or pairing credential in diagnostics.
    console.error('Extension pairing UI:', await popup.locator('body').innerText());
    console.error('Pairing form state:', await popup.evaluate(() => ({
      length: document.querySelector('#remote-pairing')?.value.length,
      disabled: document.querySelector('#remote-pairing')?.disabled,
      buttons: [...document.querySelectorAll('details button')].map(b => ({text:b.textContent,disabled:b.disabled})),
    })));
    throw error;
  }
  if (await popup.locator('details [role=alert]').count()) throw new Error('Extension authorization failed before browser tests');
  if (!taskWindow) {
    await popup.locator('#bsk-task-window-mode').selectOption('tabs');
    await popup.waitForFunction(() => {
      const select = document.querySelector('#bsk-task-window-mode');
      return select?.value === 'tabs' && !select.disabled;
    });
  } else {
    await popup.waitForFunction(() => {
      const select = document.querySelector('#bsk-task-window-mode');
      return select?.value === 'window' && !select.disabled;
    });
  }
  const initial = await worker.evaluate(async () => {
    const window = await chrome.windows.getLastFocused();
    const tabs = await chrome.tabs.query({windowId:window.id,active:true});
    return {windowId:window.id,tabId:tabs[0].id,tabIds:(await chrome.tabs.query({})).map(t=>t.id)};
  });
  const originalWindow = { windowId: initial.windowId, tabId: initial.tabId };
  process.stdout.write('ready\n');
  for await (const command of lines) {
    if (command === 'close') break;
    if (command === 'complete-help' || command === 'interrupt-window') {
      const selector = command === 'complete-help'
        ? '[data-slot="help-request-banner"][data-display-mode="full"] [data-slot="help-continue-button"]'
        : '[data-slot="control-overlay-stop-all"]';
      // Click the actual extension overlay in an isolated fixture browser.
      let clicked = false;
      const deadline = Date.now() + 5000;
      while (!clicked && Date.now() < deadline) {
        for (const page of browser.pages().filter(page => page !== popup).reverse()) {
          const button = command === 'complete-help'
            ? page.locator('[data-slot="help-request-banner"][data-display-mode="full"]')
                .filter({hasText:'Confirm this fixture step'}).locator('[data-slot="help-continue-button"]').first()
            : page.locator(selector).first();
          if (!await button.isVisible()) continue;
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
            if (!/not attached|No tab|No target/i.test(String(error))) throw error;
          }
        }
        return used;
      });
      process.stdout.write(JSON.stringify({detached:attached.length===0})+'\n');
    }
    if (command === 'check-cleanup') {
      const state = await worker.evaluate(async () => ({ids:(await chrome.tabs.query({})).map(t=>t.id), groups:await chrome.tabGroups.query({})}));
      process.stdout.write(JSON.stringify({cleaned:state.groups.length===0 && state.ids.length===initial.tabIds.length && state.ids.every(id=>initial.tabIds.includes(id))})+'\n');
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
    if (command === 'check-background') {
      if (taskWindow) {
        const valid = await worker.evaluate(async (original) => {
          const tabs = await chrome.tabs.query({});
          const groups = await chrome.tabGroups.query({});
          const taskWindows = [...new Set(tabs.filter(t => t.windowId !== original.windowId).map(t => t.windowId))];
          return tabs.find(t => t.id === original.tabId)?.active === true &&
            groups.length === 0 && taskWindows.length > 0 && taskWindows.every(id =>
              tabs.filter(t => t.windowId === id && t.active).length === 1);
        }, originalWindow);
        process.stdout.write(JSON.stringify({background:valid})+'\n');
        continue;
      }
      const current = await worker.evaluate(async () => {
        const window=await chrome.windows.getLastFocused();
        const tabs=await chrome.tabs.query({windowId:window.id,active:true});
        const windows=await chrome.windows.getAll({windowTypes:['normal']});
        const groups=await chrome.tabGroups.query({windowId:window.id});
        return {windowId:window.id,tabId:tabs[0].id,windowCount:windows.length,labeled:groups.some(g=>g.title?.startsWith('WeKnora'))};
      });
      const background = current.windowId === initial.windowId && current.tabId === initial.tabId && current.windowCount === 1 && current.labeled;
      process.stdout.write(JSON.stringify(background ? {background} : {background, initial, current}) + '\n');
    }
  }
} finally {
  await close();
}
