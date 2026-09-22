// T50 [e2e-ui] 验收：IntentGate 策略管理界面全流程（issue #21）
//
// 流程：
//   1. API 注册全新租户用户并登录（UI 走真实登录页，避开 localStorage 内部结构）；
//   2. 打开 设置 → 意图策略（/platform/settings?section=intentpolicy），跳过新手引导；
//   3. 新建策略（scope=tenant、NLC 约束、mode 默认 observe）→ 列表可见 v1；
//   4. 编辑 → 保存 → 版本 +1（v2 badge）；打开版本历史 → v1/v2 均可见；
//   5. 切 enforce → 二次确认弹窗出现（验收硬要求）→ 取消，策略保持 observe；
//   6. 全程截图到 artifacts/，断言失败退出码 1。
//
// 注：设置外壳在 DOM 里渲染两份（过渡双写），所有定位取 .first()；
// 新手引导遮罩（.guide）首次登录出现，点「跳过引导」关闭。
//
// 前置：后端 localhost:8080（lite）+ 前端 localhost:5173 已启动。
// 用法：node tests/e2e/intent-policy-ui.spec.cjs
const { chromium } = require('playwright');

const BASE = process.env.FRONTEND_URL || 'http://localhost:5173';
const API = process.env.BACKEND_URL || 'http://localhost:8080';
const SUFFIX = Date.now().toString(36);
const PASSWORD = 'Passw0rd!e2e';
const EMAIL = `t50-${SUFFIX}@e2e.local`;
const SCREENSHOT = (name) => `artifacts/t50-${name}.png`;

let failures = 0;
const ok = (msg) => console.log(`OK   ${msg}`);
const bad = (msg) => { console.log(`FAIL ${msg}`); failures += 1; };

(async () => {
  // ---- 1. 注册 + 登录（API 侧准备账号）----
  const reg = await fetch(`${API}/api/v1/auth/register`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: `t50-${SUFFIX}`, email: EMAIL, password: PASSWORD }),
  });
  if (!reg.ok) console.log('register resp', reg.status, await reg.text());
  const login = await fetch(`${API}/api/v1/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email: EMAIL, password: PASSWORD }),
  });
  if (!login.ok) { console.log('login resp', login.status, await login.text()); process.exit(1); }
  ok('register + login (api)');

  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1400, height: 900 } });

  // 新手引导（NewUserGuide）在 localStorage 标记缺失时反复弹出且跳过不持久化，
  // 直接置完成标记——本脚本验收目标与引导无关。
  await page.addInitScript(() => {
    localStorage.setItem('weknora:new-user-guide-done:v1', '1');
  });

  // ---- 2. UI 登录 ----
  await page.goto(`${BASE}/login`, { waitUntil: 'networkidle' });
  await page.locator('input[type="text"]').first().fill(EMAIL);
  await page.locator('input[type="password"]').first().fill(PASSWORD);
  await page.getByRole('button', { name: /登/ }).first().click();
  await page.waitForURL((url) => !url.pathname.includes('/login'), { timeout: 30000 });
  ok('ui login');

  // ---- 3. 进入意图策略设置页 ----
  await page.goto(`${BASE}/platform/settings?section=intentpolicy`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(800);
  const shell = page.locator('.settings-modal-shell').last();
  const createBtn = shell.getByRole('button', { name: /新建策略/ });
  await createBtn.waitFor({ state: 'visible', timeout: 20000 });
  await page.screenshot({ path: SCREENSHOT('01-list-empty'), fullPage: false });
  ok('settings page renders, create button visible');

  // ---- 4. 新建策略（mode 默认 observe）----
  await createBtn.click();
  const constraint = `e2e 单笔退款不得超过 75 (${SUFFIX})`;
  // 对话框 teleport 且外壳双写：锁定「可见且含约束表单」的那个。
  const editor = page.locator('.t-dialog:visible', { hasText: '约束' }).first();
  await editor.locator('textarea').first().waitFor({ state: 'visible', timeout: 10000 });
  await editor.locator('textarea').first().fill(constraint);
  const observeChecked = await editor.locator('.t-radio').filter({ hasText: '观察' }).first()
    .evaluate((el) => el.classList.contains('t-is-checked'));
  if (observeChecked) ok('mode defaults to observe'); else bad('mode default is not observe');
  await page.screenshot({ path: SCREENSHOT('02-create-dialog'), fullPage: false });
  await editor.getByRole('button', { name: /保存/ }).first().click();
  await page.waitForTimeout(1500);

  const card = shell.locator('.lineage-card', { hasText: constraint });
  if (await card.count() === 1) ok('created policy visible in list'); else bad('created policy not visible in list');
  if (await card.first().locator('.lineage-card__badges').getByText('v1', { exact: true }).count() === 1) {
    ok('version badge v1');
  } else bad('version badge v1 missing');
  await page.screenshot({ path: SCREENSHOT('03-created-v1'), fullPage: false });

  // ---- 5. 编辑 → v2 ----
  await card.first().getByRole('button', { name: /编辑/ }).click();
  await editor.locator('textarea').first().fill(`${constraint}（修订）`);
  await editor.getByRole('button', { name: /保存/ }).first().click();
  await page.waitForTimeout(1500);
  const card2 = shell.locator('.lineage-card', { hasText: `${constraint}（修订）` }).first();
  if (await card2.count() === 1) ok('edited constraint visible'); else bad('edited constraint not visible');
  if (await card2.locator('.lineage-card__badges').getByText('v2', { exact: true }).count() === 1) {
    ok('version badge v2 after edit');
  } else bad('v2 badge missing');
  await page.screenshot({ path: SCREENSHOT('04-edited-v2'), fullPage: false });

  // ---- 6. 版本历史 v1/v2 ----
  await card2.getByRole('button', { name: /版本历史/ }).click();
  const history = card2.locator('.version-history');
  await history.waitFor({ state: 'visible', timeout: 5000 });
  const histText = await history.innerText();
  if (histText.includes('v2') && histText.includes('v1')) ok('version history shows v1 and v2');
  else bad(`version history missing v1/v2: ${histText}`);
  await page.screenshot({ path: SCREENSHOT('05-version-history'), fullPage: false });

  // ---- 7. 切 enforce → 二次确认 ----
  await card2.getByRole('button', { name: /编辑/ }).click();
  await editor.locator('.t-radio').filter({ hasText: '强制执行' }).first().click();
  await editor.getByRole('button', { name: /保存/ }).first().click();
  const confirmDialog = page.locator('.t-dialog', { hasText: /强制执行模式/ }).last();
  await confirmDialog.waitFor({ state: 'visible', timeout: 5000 });
  ok('enforce switch triggers confirm dialog');
  await page.screenshot({ path: SCREENSHOT('06-enforce-confirm'), fullPage: false });
  await confirmDialog.getByRole('button', { name: /取消/ }).click();
  await page.waitForTimeout(800);
  const badges = card2.locator('.lineage-card__badges');
  if (await badges.getByText('观察', { exact: true }).count() >= 1) ok('cancel keeps observe mode');
  else bad('cancel did not keep observe mode');

  await browser.close();
  console.log(failures === 0 ? 'ALL PASS' : `${failures} FAILURES`);
  process.exit(failures === 0 ? 0 : 1);
})().catch((err) => {
  console.error('E2E ERROR', err.message);
  process.exit(1);
});
