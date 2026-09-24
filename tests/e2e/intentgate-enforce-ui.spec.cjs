// T40 [e2e-ui] 验收：enforce deny 在真实对话中可见（issue #17）
//
// 流程：
//   1. API 注册全新租户用户（内置 Kimi 模型对全租户可用）；
//   2. API 创建 smart-reasoning agent（强制先调 search_conversations）+
//      enforce 策略（rule_expr 恒 false → 每次工具调用必 deny）+ 会话；
//   3. UI 登录 → 打开该会话的对话页 → 发送引导工具调用的问题；
//   4. 断言：agent 的回复正文解释"被策略拒绝"（工具被 enforce 拦截后
//      agent 可见 DeniedError 理由并自我纠错——验收硬要求）；
//   5. 截图证据到 artifacts/。
//
// 前置：后端 :8080 跑着含 T40 的二进制；前端 :5173。
// 用法：node tests/e2e/intentgate-enforce-ui.spec.cjs
const { chromium } = require('playwright');

const BASE = process.env.FRONTEND_URL || 'http://localhost:5173';
const API = process.env.BACKEND_URL || 'http://localhost:8080';
const SUFFIX = Date.now().toString(36);
const PASSWORD = 'Passw0rd!e2e';
const EMAIL = `t40-${SUFFIX}@e2e.local`;
const SCREENSHOT = (name) => `artifacts/t40-${name}.png`;

let failures = 0;
const ok = (msg) => console.log(`OK   ${msg}`);
const bad = (msg) => { console.log(`FAIL ${msg}`); failures += 1; };


(async () => {
  // ---- 1. 注册 + 登录（API）----
  const reg = await fetch(`${API}/api/v1/auth/register`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: `t40-${SUFFIX}`, email: EMAIL, password: PASSWORD }),
  });
  if (!reg.ok) console.log('register resp', reg.status, await reg.text());
  const login = await fetch(`${API}/api/v1/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email: EMAIL, password: PASSWORD }),
  });
  if (!login.ok) { console.log('login resp', login.status, await login.text()); process.exit(1); }
  const { token } = await login.json();
  const auth = { Authorization: `Bearer ${token}` };
  ok('register + login (api)');

  // ---- 2. API 建 agent + enforce 策略 + 会话 ----
  const agentResp = await fetch(`${API}/api/v1/agents`, {
    method: 'POST',
    headers: { ...auth, 'Content-Type': 'application/json' },
    body: JSON.stringify({
      name: `t40-ui-${SUFFIX}`,
      config: {
        agent_mode: 'smart-reasoning',
        model_id: 'builtin-kimi-coding',
        allowed_tools: ['search_conversations'],
        system_prompt: '你必须先调用 search_conversations 工具，再基于工具返回作答；禁止不调用工具直接回答。',
      },
    }),
  });
  const agentData = await agentResp.json();
  const agentId = agentData.data && agentData.data.id;
  if (!agentId) { console.log('agent create failed', JSON.stringify(agentData).slice(0, 300)); process.exit(1); }

  const polResp = await fetch(`${API}/api/v1/intent-policies`, {
    method: 'POST',
    headers: { ...auth, 'Content-Type': 'application/json' },
    body: JSON.stringify({
      scope_type: 'tenant',
      scope_ref: '',
      constraint_text: 'T40 e2e：任何工具调用都视为违反约束',
      rule_expr: '1 > 2',
      risk_tier: 'low',
      mode: 'enforce',
    }),
  });
  const polData = await polResp.json();
  if (!polData.data || !polData.data.id) { console.log('policy create failed', JSON.stringify(polData).slice(0, 300)); process.exit(1); }

  const sessResp = await fetch(`${API}/api/v1/sessions`, {
    method: 'POST',
    headers: { ...auth, 'Content-Type': 'application/json' },
    body: JSON.stringify({ title: 't40-e2e', description: 'T40 enforce e2e' }),
  });
  const sessData = await sessResp.json();
  const sessionId = sessData.data && sessData.data.id;
  if (!sessionId) { console.log('session create failed', JSON.stringify(sessData).slice(0, 300)); process.exit(1); }
  ok(`agent/policy/session ready (policy=${polData.data.id} session=${sessionId})`);

  // ---- 3. UI 登录并打开对话页 ----
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

  await page.goto(`${BASE}/platform/chat/${sessionId}`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(1500);
  ok('chat page open');

  // ---- 4. 用 API 触发 agent-chat（UI 发送路径在 T50 已验；本票验收的是
  //         deny 后的回复在对话 UI 可见），带重试防模型直接作答 ----
  const question = '请先调用 search_conversations 工具（query 参数填"1+1"），引用工具返回的结果，然后告诉我 1+1 等于几。';
  let replied = false;
  for (let attempt = 1; attempt <= 3 && !replied; attempt++) {
    await fetch(`${API}/api/v1/agent-chat/${sessionId}`, {
      method: 'POST',
      headers: { ...auth, 'Content-Type': 'application/json' },
      body: JSON.stringify({ query: question, agent_id: agentId, disable_title: true }),
    }).catch(() => {});
    // 轮询会话最后一条 assistant 消息正文是否提到策略拒绝（最长 90s）。
    for (let i = 0; i < 30; i++) {
      await new Promise((r) => setTimeout(r, 3000));
      const msgs = await fetch(`${API}/api/v1/messages/${sessionId}/load`, { headers: auth })
        .then((r) => (r.ok ? r.json() : null)).catch(() => null);
      const list = (msgs && (msgs.data || msgs.messages || (Array.isArray(msgs) ? msgs : []))) || [];
      const assistant = [...list].reverse().find((m) => m.role === 'assistant' && m.content);
      if (assistant && /策略|拒绝/.test(assistant.content)) {
        replied = true;
        break;
      }
    }
    if (!replied) console.log(`attempt ${attempt}: assistant 未提及拒绝，重试…`);
  }
  if (!replied) { bad('agent reply (api) never mentioned the denial'); }
  else { ok('agent reply (api) explains the policy denial'); }

  // ---- 5. 刷新对话页，断言拒绝解释渲染在 UI 气泡里 ----
  await page.reload({ waitUntil: 'networkidle' });
  await page.waitForTimeout(2000);
  const denial = page.locator('text=/策略|拒绝/').first();
  let seen = false;
  try {
    await denial.waitFor({ state: 'visible', timeout: 20000 });
    seen = true;
  } catch { seen = false; }
  await page.waitForTimeout(800);
  await page.screenshot({ path: SCREENSHOT('01-chat-denial'), fullPage: false });
  if (seen) ok('denial explanation rendered in chat UI');
  else bad('denial explanation not visible in chat UI');

  await browser.close();
  console.log(failures === 0 ? 'ALL PASS' : `${failures} FAILURES`);
  process.exit(failures === 0 ? 0 : 1);
})().catch((err) => {
  console.error('E2E ERROR', err.message);
  process.exit(1);
});
