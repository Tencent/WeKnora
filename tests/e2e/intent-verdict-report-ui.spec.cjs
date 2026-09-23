// T51 [e2e-ui] 验收：IntentGate 判定报表页（issue #22）
//
// 流程：
//   1. API 注册新租户 → 建策略（rule_expr 恒 false → 每次工具调用必 deny）
//      → 建 agent（kimi + search_conversations + 强制先调工具）；
//   2. 触发 agent-chat（带重试直到至少 1 条 verdict 落库）；
//   3. sqlite 直查该策略的 verdict 分布（验收的对账基准）；
//   4. UI 登录 → 设置 → 判定报表 → 断言：
//      - deny 分布卡数字 == sqlite 直查计数（验收硬要求：与 psql/sqlite
//        直查结果一致）；
//      - 总计 == 分布和；
//      - 点击单条 verdict 展开可见 reason（含策略约束原文）；
//   5. 截图证据到 artifacts/。
//
// 前置：后端 :8080 含 T51 API；前端 :5173。
// 用法：node tests/e2e/intent-verdict-report-ui.spec.cjs
const { chromium } = require('playwright');
const { execSync } = require('child_process');

const BASE = process.env.FRONTEND_URL || 'http://localhost:5173';
const API = process.env.BACKEND_URL || 'http://localhost:8080';
const SUFFIX = Date.now().toString(36);
const PASSWORD = 'Passw0rd!e2e';
const EMAIL = `t51-${SUFFIX}@e2e.local`;
const DB = process.env.T51_DB_PATH || 'C:/Users/Administrator/orca/WeKnora/data/weknora.db';
const SCREENSHOT = (name) => `artifacts/t51-${name}.png`;

let failures = 0;
const ok = (msg) => console.log(`OK   ${msg}`);
const bad = (msg) => { console.log(`FAIL ${msg}`); failures += 1; };

// sqlite 直查（验收对账基准），走 python 避免 node 缺 sqlite 驱动。
function dbScalar(sql) {
  const out = execSync(
    `DB_PATH=${DB} SQL=${JSON.stringify(sql)} python -c "import os,sqlite3,sys;sys.stdout.reconfigure(encoding='utf-8');c=sqlite3.connect('file:%s?mode=ro'%os.environ['DB_PATH'],uri=True);r=c.execute(os.environ['SQL']).fetchone();print('' if r is None else r[0])"`,
    { shell: 'C:/Program Files/Git/usr/bin/bash.exe' },
  ).toString().trim();
  return out;
}

(async () => {
  // ---- 1. 注册 + 建策略 + 建 agent ----
  await fetch(`${API}/api/v1/auth/register`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: `t51-${SUFFIX}`, email: EMAIL, password: PASSWORD }),
  });
  const login = await fetch(`${API}/api/v1/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email: EMAIL, password: PASSWORD }),
  });
  const { token } = await login.json();
  const auth = { Authorization: `Bearer ${token}` };
  ok('register + login (api)');

  const pol = await fetch(`${API}/api/v1/intent-policies`, {
    method: 'POST',
    headers: { ...auth, 'Content-Type': 'application/json' },
    body: JSON.stringify({
      scope_type: 'tenant', scope_ref: '',
      constraint_text: 'T51 报表验收：任何工具调用都视为违反约束',
      rule_expr: '1 > 2', risk_tier: 'low', mode: 'observe',
    }),
  }).then((r) => r.json());
  const policyId = pol.data && pol.data.id;
  if (!policyId) { console.log('policy failed', JSON.stringify(pol).slice(0, 200)); process.exit(1); }

  const agent = await fetch(`${API}/api/v1/agents`, {
    method: 'POST',
    headers: { ...auth, 'Content-Type': 'application/json' },
    body: JSON.stringify({
      name: `t51-${SUFFIX}`,
      config: {
        agent_mode: 'smart-reasoning',
        model_id: 'builtin-kimi-coding',
        allowed_tools: ['search_conversations'],
        system_prompt: '你必须先调用 search_conversations 工具，再基于工具返回作答；禁止不调用工具直接回答。',
      },
    }),
  }).then((r) => r.json());
  const agentId = agent.data && agent.data.id;
  if (!agentId) { console.log('agent failed', JSON.stringify(agent).slice(0, 200)); process.exit(1); }
  ok(`policy + agent ready (${policyId})`);

  // ---- 2. 触发判定（重试至 ≥1 条 verdict）----
  const question = '请先调用 search_conversations 工具（query 参数填"1+1"），然后告诉我 1+1 等于几。';
  let denyCount = 0;
  for (let attempt = 1; attempt <= 3 && denyCount < 1; attempt++) {
    const sess = await fetch(`${API}/api/v1/sessions`, {
      method: 'POST',
      headers: { ...auth, 'Content-Type': 'application/json' },
      body: JSON.stringify({ title: `t51-${attempt}`, description: 'T51' }),
    }).then((r) => r.json()).then((j) => j.data && j.data.id);
    await fetch(`${API}/api/v1/agent-chat/${sess}`, {
      method: 'POST',
      headers: { ...auth, 'Content-Type': 'application/json' },
      body: JSON.stringify({ query: question, agent_id: agentId, disable_title: true }),
    }).catch(() => {});
    for (let i = 0; i < 30; i++) {
      await new Promise((r) => setTimeout(r, 3000));
      denyCount = Number(dbScalar(
        `SELECT count(*) FROM intent_verdicts WHERE policy_id = '${policyId}' AND verdict = 'deny'`,
      ) || 0);
      if (denyCount >= 1) break;
    }
  }
  if (denyCount < 1) { console.log('FAIL 未能触发任何 verdict'); process.exit(1); }
  ok(`verdicts landed (deny=${denyCount})`);

  // ---- 3. sqlite 对账基准 ----
  const totalSql = `SELECT count(*) FROM intent_verdicts WHERE policy_id = '${policyId}'`;
  const sqliteTotal = Number(dbScalar(totalSql) || 0);
  ok(`sqlite 直查：deny=${denyCount} total=${sqliteTotal}`);

  // ---- 4. UI：报表页数字对账 + 下钻 ----
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1400, height: 900 } });
  await page.addInitScript(() => {
    localStorage.setItem('weknora:new-user-guide-done:v1', '1');
  });
  await page.goto(`${BASE}/login`, { waitUntil: 'networkidle' });
  await page.locator('input[type="text"]').first().fill(EMAIL);
  await page.locator('input[type="password"]').first().fill(PASSWORD);
  await page.getByRole('button', { name: /登/ }).first().click();
  await page.waitForURL((url) => !url.pathname.includes('/login'), { timeout: 30000 });
  ok('ui login');

  await page.goto(`${BASE}/platform/settings?section=intentverdict`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(2000);

  const denyCard = page.locator('.stat-card[data-verdict="deny"] .stat-card__num').last();
  await denyCard.waitFor({ state: 'visible', timeout: 20000 });
  const uiDeny = Number((await denyCard.innerText()).trim());
  if (uiDeny === denyCount) ok(`报表 deny 分布数字 ${uiDeny} == sqlite 直查 ${denyCount}`);
  else bad(`报表 deny=${uiDeny} != sqlite=${denyCount}`);

  const totalCard = page.locator('.stat-card--total .stat-card__num').last();
  const uiTotal = Number((await totalCard.innerText()).trim());
  if (uiTotal === sqliteTotal) ok(`报表总计 ${uiTotal} == sqlite 直查 ${sqliteTotal}`);
  else bad(`报表 total=${uiTotal} != sqlite=${sqliteTotal}`);

  // 下钻：展开第一条 verdict，断言 reason 可见
  await page.locator('.verdict-row__head').last().click();
  const detail = page.locator('.verdict-row__detail').last();
  await detail.waitFor({ state: 'visible', timeout: 5000 });
  const reasonText = await detail.innerText();
  if (reasonText.includes('T51 报表验收')) ok('下钻可见 reason（含策略约束原文）');
  else bad(`下钻 reason 不含约束原文: ${reasonText.slice(0, 120)}`);
  await page.screenshot({ path: SCREENSHOT('01-report'), fullPage: false });

  await browser.close();
  console.log(failures === 0 ? 'ALL PASS' : `${failures} FAILURES`);
  process.exit(failures === 0 ? 0 : 1);
})().catch((err) => {
  console.error('E2E ERROR', err.message);
  process.exit(1);
});
