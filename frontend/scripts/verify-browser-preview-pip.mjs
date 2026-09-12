// Opt-in integration test: a real PiP window and the production Vue component,
// with a simulated browser-task API. No extension, model or user account is used.
import assert from 'node:assert/strict';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createServer } from 'vite';
import vue from '@vitejs/plugin-vue';

const executablePath = process.env.BROWSERSKILL_TEST_CHROMIUM;
const playwrightPath = process.env.BROWSERSKILL_TEST_PLAYWRIGHT;
assert.ok(executablePath && playwrightPath, 'Set BROWSERSKILL_TEST_CHROMIUM and BROWSERSKILL_TEST_PLAYWRIGHT');
const { chromium } = await import(pathToFileURL(playwrightPath).href);
const root = fileURLToPath(new URL('../', import.meta.url));
const cacheDir = await mkdtemp(join(tmpdir(), 'weknora-pip-vite-'));
const status = { enabled: true, selected: true, connected: true, task_id: 'fixture', paused: false, idle: false, action: 'observe', action_elapsed_ms: 1000 };
const calls = [];
const entry = `
import {createApp,h,ref} from 'vue';
import {createPinia} from 'pinia';
import TDesign from 'tdesign-vue-next';
import 'tdesign-vue-next/es/style/index.css';
import i18n from '/src/i18n/index.ts';
import Preview from '/src/views/chat/components/BrowserTaskPreview.vue';
const session = ref('pip-test');
const app = createApp({setup(){return ()=>h('main',{style:'position:relative;width:900px;height:700px'},[
  h('button',{onClick:()=>session.value = session.value === 'pip-test' ? 'other-test' : 'pip-test'},'Switch conversation'),
  h(Preview,{key:session.value,sessionId:session.value})
])}});
app.use(createPinia()).use(i18n).use(TDesign).mount('#app');
`;
const server = await createServer({
  root, cacheDir, configFile: false, plugins: [vue(), {
    name: 'pip-fixture',
    resolveId(id) { if (id === '/pip-fixture.js') return '\0pip-fixture'; },
    load(id) { if (id === '\0pip-fixture') return entry; },
    configureServer(server) {
      server.middlewares.use(async (req, res, next) => {
        if (req.url === '/pip-fixture.html') {
          res.setHeader('Content-Type', 'text/html');
          res.end(await server.transformIndexHtml(req.url, '<!doctype html><html><head></head><body><div id="app"></div><script type="module" src="/pip-fixture.js"></script></body></html>'));
        } else if (req.url?.match(/^\/api\/v1\/sessions\/(pip-test|other-test)\/local-browser$/)) {
          let body = '';
          for await (const chunk of req) body += chunk;
          const action = body ? JSON.parse(body).action : 'status';
          calls.push({ action, url: req.url });
          if (action === 'pause') status.paused = true;
          if (action === 'resume') status.paused = false;
          if (action === 'stop') status.selected = false;
          const data = action === 'preview' ? {
            // Valid one-pixel PNG: this test verifies delivery, not image recognition.
            image_base64: 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aD1sAAAAASUVORK5CYII=',
            format: 'png', captured_at: new Date().toISOString(),
          } : status;
          res.setHeader('Content-Type', 'application/json');
          res.end(JSON.stringify({ success: true, data }));
        } else next();
      });
    },
  }], resolve: { alias: { '@': root + 'src' } },
  server: { host: '127.0.0.1', port: 0 },
});
let browser;
async function until(check, label) {
  const deadline = Date.now() + 15000;
  while (!await check()) {
    assert.ok(Date.now() < deadline, `Timed out: ${label}`);
    await new Promise(resolve => setTimeout(resolve, 100));
  }
}
try {
  await server.listen();
  const url = server.resolvedUrls.local[0] + 'pip-fixture.html';
  browser = await chromium.launch({ executablePath, headless: false });
  const context = await browser.newContext();
  await context.addInitScript(() => localStorage.setItem('locale', 'en-US'));
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.goto(url);
  const preview = page.locator('.browser-task-preview');
  const open = page.getByRole('button', { name: 'Pop out preview', exact: true });
  await open.waitFor();
  await page.waitForFunction(() => document.querySelector('.preview-image img')?.naturalWidth > 0);
  const heading = await page.locator('.preview-heading').boundingBox();
  await page.mouse.move(heading.x + 30, heading.y + 15);
  await page.mouse.down();
  await page.mouse.move(heading.x - 120, heading.y - 70);
  await page.mouse.up();
  const savedPosition = await preview.evaluate(el => ({ left: el.style.left, top: el.style.top }));
  assert.ok(savedPosition.left && savedPosition.top, 'preview was dragged before pop-out');
  // A rejected request must leave the existing UI usable and allow a retry.
  await page.evaluate(() => {
    const api = window.documentPictureInPicture;
    const real = api.requestWindow.bind(api);
    api.requestWindow = () => { api.requestWindow = real; return Promise.reject(new Error('fixture rejection')); };
  });
  await open.click();
  await page.getByRole('alert').filter({ hasText: 'Could not open the floating window' }).waitFor();
  await open.click();
  await page.waitForFunction(() => window.documentPictureInPicture.window?.document.querySelector('.is-pip'));
  assert.equal(await preview.count(), 0, 'only one preview is rendered');
  assert.equal(await page.evaluate(() => {
    const w = documentPictureInPicture.window;
    return w.getComputedStyle(w.document.querySelector('.is-pip')).position;
  }), 'static', 'scoped component styles are available in PiP');
  // The live theme follows the opener, without reopening the window.
  await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'dark'));
  await page.waitForFunction(() => documentPictureInPicture.window.document.documentElement.getAttribute('data-theme') === 'dark');
  const other = await context.newPage();
  await other.goto('about:blank');
  await other.bringToFront();
  // Chrome may keep the opener visible while its document PiP is visible.
  // First verify real tab switching, then explicitly exercise the hidden branch.
  const switched = calls.filter(call => call.action === 'preview').length;
  await until(() => calls.filter(call => call.action === 'preview').length >= switched + 2, 'preview after switching tabs');
  await page.evaluate(() => {
    Object.defineProperty(document, 'hidden', { configurable: true, get: () => true });
    document.dispatchEvent(new Event('visibilitychange'));
  });
  const before = calls.filter(call => call.action === 'preview').length;
  await until(() => calls.filter(call => call.action === 'preview').length >= before + 2, 'preview updates with hidden opener');
  await page.evaluate(() => { delete document.hidden; document.dispatchEvent(new Event('visibilitychange')); });
  const clickPip = name => page.evaluate(name => {
    const button = [...documentPictureInPicture.window.document.querySelectorAll('button')].find(button => button.textContent.trim() === name || button.getAttribute('aria-label') === name);
    if (!button) throw new Error('Missing PiP button: ' + name);
    button.click();
  }, name);
  await clickPip('Pause browser');
  await until(() => status.paused, 'pause command');
  await page.waitForFunction(() => documentPictureInPicture.window.document.body.textContent.includes('Resume browser'));
  await clickPip('Resume browser');
  await until(() => !status.paused, 'resume command');
  await page.waitForFunction(() => !documentPictureInPicture.window.document.querySelector('.preview-actions button').disabled);
  await clickPip('Return to conversation');
  await until(() => preview.count(), 'PiP returns to page');
  assert.equal(status.selected, true, 'closing PiP does not stop browser work');
  assert.deepEqual(await preview.evaluate(el => ({ left: el.style.left, top: el.style.top })), savedPosition, 'restore the dragged page position');
  await page.bringToFront();
  await open.click();
  await page.waitForFunction(() => documentPictureInPicture.window?.document.querySelector('.is-pip'));
  await page.evaluate(() => documentPictureInPicture.window.close());
  await preview.waitFor();
  await open.click();
  await page.waitForFunction(() => documentPictureInPicture.window?.document.querySelector('.is-pip'));
  await page.getByRole('button', { name: 'Switch conversation', exact: true }).click();
  await page.waitForFunction(() => !documentPictureInPicture.window);
  await preview.waitFor();
  await open.click();
  await page.waitForFunction(() => documentPictureInPicture.window?.document.querySelector('.is-pip'));
  await clickPip('End browser task');
  await until(() => !status.selected, 'stop command');
  await page.waitForFunction(() => !documentPictureInPicture.window);
  assert.equal(await preview.count(), 0);
  assert.ok(calls.some(call => call.action === 'stop' && call.url.includes('/other-test/')), 'new conversation controls only its own task');
  assert.deepEqual(errors, [], 'no Vue or cross-document runtime errors');
  // Feature detection must keep the current page preview on unsupported browsers.
  status.selected = true;
  const unsupported = await context.newPage();
  await unsupported.addInitScript(() => Object.defineProperty(window, 'documentPictureInPicture', { value: undefined }));
  await unsupported.goto(url);
  await unsupported.locator('.browser-task-preview').waitFor();
  assert.equal(await unsupported.getByRole('button', { name: 'Pop out preview', exact: true }).count(), 0);
  console.log('PASS: real PiP open/restore, scoped styles/theme, background frames, shared pause/resume/stop, session teardown, rejection retry and unsupported fallback');
} finally {
  await browser?.close();
  await server.close();
  await rm(cacheDir, { recursive: true, force: true });
}
