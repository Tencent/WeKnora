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
  const popup = await browser.newPage();
  await popup.goto(new URL('popup.html', worker.url()).href);
  await popup.locator('details summary').click();
  await popup.locator('#remote-pairing').fill(pairing);
  await popup.locator('details button').first().click();
  await popup.locator('#remote-pairing').waitFor({ state: 'visible' });
  const initial = await worker.evaluate(async () => {
    const window = await chrome.windows.getLastFocused();
    const tabs = await chrome.tabs.query({windowId:window.id,active:true});
    return {windowId:window.id,tabId:tabs[0].id};
  });
  process.stdout.write('ready\n');
  for await (const command of lines) {
    if (command === 'close') break;
    if (command === 'check-background') {
      const current = await worker.evaluate(async () => {
        const window=await chrome.windows.getLastFocused();
        const tabs=await chrome.tabs.query({windowId:window.id,active:true});
        const windows=await chrome.windows.getAll({windowTypes:['normal']});
        const groups=await chrome.tabGroups.query({windowId:window.id});
        return {windowId:window.id,tabId:tabs[0].id,windowCount:windows.length,labeled:groups.some(g=>g.title?.startsWith('WeKnora'))};
      });
      process.stdout.write(JSON.stringify({ background: current.windowId === initial.windowId && current.tabId === initial.tabId && current.windowCount === 1 && current.labeled }) + '\n');
    }
  }
} finally {
  await close();
}
