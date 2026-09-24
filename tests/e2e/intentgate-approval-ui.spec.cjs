// T41 [e2e-ui] 验收：审批卡片 UI 点击（issue #18）
//
// Run A：对话中触发需审批调用 → 审批卡片出现（描述含策略理由）→ 点「批准」
//         → 工具执行成功（页面可见 echo 结果）。
// 触发路径（issue #35）：模型按名直注册工具（mcp_<服务>_<工具>）调用——
// 哨兵策略 scope_ref=mcp_<服务名前缀>_* 只命中直注册名，不命中 call_mcp_tool
// 代理；若模型走代理路径，哨兵不触发、本用例不产卡片（属策略 scope 语义，
// 不是本用例的回归）。
// 前置：后端 :8080（含 T41+#31）、前端 :5173、mcp-echo-server :8765。
// 用法：node tests/e2e/intentgate-approval-ui.spec.cjs
const { chromium } = require('playwright');

const BASE = process.env.FRONTEND_URL || 'http://localhost:5173';
const API = process.env.BACKEND_URL || 'http://localhost:8080';
const SUFFIX = Date.now().toString(36);
const PASSWORD = 'Passw0rd!e2e';
const EMAIL = `t41ui-${SUFFIX}@e2e.local`;
const SCREENSHOT = (name) => `artifacts/t41-${name}.png`;

let failures = 0;
const ok = (msg) => console.log(`OK   ${msg}`);
const bad = (msg) => { console.log(`FAIL ${msg}`); failures += 1; };

(async () => {
  // ---- API 侧准备：用户 + MCP 服务 + 哨兵策略 + agent + 会话 ----
  await fetch(`${API}/api/v1/auth/register`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: `t41ui-${SUFFIX}`, email: EMAIL, password: PASSWORD }),
  });
  const { token } = await fetch(`${API}/api/v1/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email: EMAIL, password: PASSWORD }),
  }).then((r) => r.json());
  const auth = { Authorization: `Bearer ${token}` };

  const svc = await fetch(`${API}/api/v1/mcp-services`, {
    method: 'POST',
    headers: { ...auth, 'Content-Type': 'application/json' },
    body: JSON.stringify({
      name: `echo-ui-${SUFFIX}`, description: 'T41 ui',
      transport_type: 'http-streamable', url: 'http://localhost:8765/mcp',
    }),
  }).then((r) => r.json());
  const svcId = svc.data && svc.data.id;
  if (!svcId) { console.log('mcp service failed', JSON.stringify(svc).slice(0, 200)); process.exit(1); }

  // tool 级策略（scope_ref 无冒号 = 按工具名匹配任意 service）：只命中
  // 真实 MCP 工具（mcp_<服务名>_<工具>），discover_mcp_tools 目录工具
  // 不受哨兵管辖——否则目录调用会被「无审批通道」放行并干扰模型流程。
  const svcSlug = `echo_ui_${SUFFIX}`.toLowerCase();
  await fetch(`${API}/api/v1/intent-policies`, {
    method: 'POST',
    headers: { ...auth, 'Content-Type': 'application/json' },
    body: JSON.stringify({
      scope_type: 'tool', scope_ref: `mcp_${svcSlug}_*`,
      constraint_text: 'T41 UI 验收：管辖调用一律人工审批',
      rule_expr: 'require_approval', risk_tier: 'low', mode: 'enforce',
    }),
  });

  const agent = await fetch(`${API}/api/v1/agents`, {
    method: 'POST',
    headers: { ...auth, 'Content-Type': 'application/json' },
    body: JSON.stringify({
      name: `t41ui-${SUFFIX}`,
      config: {
        agent_mode: 'smart-reasoning',
        model_id: 'builtin-kimi-coding',
        allowed_tools: ['discover_mcp_tools', 'call_mcp_tool'],
        mcp_selection_mode: 'selected',
        mcp_services: [svcId],
        system_prompt: `你必须依次完成三步：第一步调用 discover_mcp_tools（参数 {"mode":"list_tools","server_id":"${svcId}"}），从返回结果中取 echo 工具的 function_name 字段（mcp_ 开头的直注册名字）；第二步用 mode=\"describe\" describe 该工具（server_id 同上，tool_name=\"echo\"）；第三步直接用第一步拿到的 function_name 以参数 {"text":"hello"} 调用该工具。禁止调用 call_mcp_tool，禁止不调用工具直接作答。`,
      },
    }),
  }).then((r) => r.json());
  const agentId = agent.data && agent.data.id;

  // 快速问答的「对话模型」就绪检查读 builtin-quick-answer 的 config.model_id
  //（新租户跳过初始化向导故为空，发送被前置拦截）。PUT /agents/:id 是
  // 全量 config 替换——先取回合并再写回。
  const builtins = await fetch(`${API}/api/v1/agents`, { headers: auth }).then((r) => r.json());
  const qa = (builtins.data || []).find((a) => a.id === 'builtin-quick-answer');
  if (qa && !(qa.config && qa.config.model_id)) {
    await fetch(`${API}/api/v1/agents/builtin-quick-answer`, {
      method: 'PUT',
      headers: { ...auth, 'Content-Type': 'application/json' },
      body: JSON.stringify({ config: { ...qa.config, model_id: 'builtin-kimi-coding' } }),
    });
  }

  const sess = await fetch(`${API}/api/v1/sessions`, {
    method: 'POST',
    headers: { ...auth, 'Content-Type': 'application/json' },
    body: JSON.stringify({ title: 't41-ui', description: 'T41' }),
  }).then((r) => r.json()).then((j) => j.data && j.data.id);
  ok(`setup done (svc=${svcId} session=${sess})`);

  // ---- UI：登录 → 打开会话 → 发消息 → 等审批卡片 → 点批准 ----
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1400, height: 900 } });
  // 注意：agentId 必须在此刻注入（值已知于 setup 阶段）。
  const seedInit = (agentId) => {
    localStorage.setItem('weknora:new-user-guide-done:v1', '1');
    localStorage.setItem('weknora_last_chat_model_id', 'builtin-kimi-coding');
    // chat 页的智能体选择来自 pinia settings store 的持久化
    // （WeKnora_settings.selectedAgentId，见 settingsStorage.ts），
    // query 参数不生效——直接播种让发送走 agent-chat 路径。
    localStorage.setItem('WeKnora_settings', JSON.stringify({
      selectedAgentId: agentId,
      isAgentEnabled: true,
      selectedTags: [],
      selectedMCPServices: [],
      selectedSkills: [],
    }));
  };
  await page.addInitScript(seedInit, agentId);
  await page.goto(`${BASE}/login`, { waitUntil: 'networkidle' });
  await page.locator('input[type="text"]').first().fill(EMAIL);
  await page.locator('input[type="password"]').first().fill(PASSWORD);
  await page.getByRole('button', { name: /登/ }).first().click();
  await page.waitForURL((url) => !url.pathname.includes('/login'), { timeout: 30000 });
  ok('ui login');

  await page.goto(`${BASE}/platform/chat/${sess}`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(1200);
  const composer = page.locator('textarea').last();
  await composer.fill('请按系统指令完成工具调用');
  // Enter 在该输入框走 mention 分支不提交；发送按钮是 button.send-btn。
  const sendBtn = page.locator('button.send-btn').last();
  await sendBtn.waitFor({ state: 'visible', timeout: 10000 });
  await sendBtn.click();
  ok('question sent via .send-btn');

  // 审批卡片：SSE 事件渲染，描述含策略理由。
  const card = page.locator('text=意图策略要求人工审批').last();
  try {
    await card.waitFor({ state: 'visible', timeout: 180000 });
    ok('approval card visible with policy reason');
  } catch {
    bad('approval card not visible in 180s');
    await page.screenshot({ path: SCREENSHOT('01-no-card'), fullPage: false });
    await browser.close();
    process.exit(1);
  }
  await page.screenshot({ path: SCREENSHOT('01-card'), fullPage: false });

  // 点「批准」。按钮文案候选：批准/同意/通过/Approve。
  // ToolApprovalCard.vue：批准按钮是 .inline-action.is-primary。
  const approveBtn = page.locator('.inline-action.is-primary').last();
  try {
    await approveBtn.click({ timeout: 10000 });
    ok('approve clicked');
  } catch {
    bad('approve button not found/clickable');
    await page.screenshot({ path: SCREENSHOT('02-no-approve-btn'), fullPage: false });
    await browser.close();
    process.exit(1);
  }

  // 工具执行成功：页面出现 echo:hello（echo server 原样回显）。
  const echoResult = page.locator('text=echo:hello').last();
  try {
    await echoResult.waitFor({ state: 'visible', timeout: 120000 });
    ok('tool executed after approval (echo:hello visible)');
  } catch {
    bad('echo:hello not visible after approval');
  }
  await page.waitForTimeout(800);
  await page.screenshot({ path: SCREENSHOT('02-approved'), fullPage: false });

  await browser.close();
  console.log(failures === 0 ? 'ALL PASS' : `${failures} FAILURES`);
  process.exit(failures === 0 ? 0 : 1);
})().catch((err) => {
  console.error('E2E ERROR', err.message);
  process.exit(1);
});
