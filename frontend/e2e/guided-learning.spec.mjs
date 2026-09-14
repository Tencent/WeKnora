/**
 * Real-runtime Playwright E2E, using Node's test runner and local Playwright.
 *
 *   cd frontend && npm ci && npx playwright install chromium
 *   npm run test:e2e:config           offline validation only, no live fixtures
 *   npm run test:e2e:guided-learning real calls against explicitly seeded data
 *
 * GL_RUNTIME_DIR defaults to repo/.runtime; subdirectories there are allowed.
 * Paths are checkout-relative or absolute, never symlinks or foreign paths.
 * Reads seed-accounts.json and evidence/seed.json from that selected runtime.
 * API comes from saved api_base; GL_API_BASE must normalize to that same origin.
 * Frontend comes from saved frontend_base (legacy default 127.0.0.1:25173),
 * with an explicit GL_FRONTEND_BASE loopback override. HTTP loopback only:
 * 127.0.0.1, localhost, or [::1], at any valid port. No URL credentials or
 * query/fragment/path; trailing slash and API /api/v1 suffix are normalized.
 * Only disposable fixtures are admitted. Private accounts are READ ONLY, 0600;
 * fresh API login + in-memory storageState, never browser credential inputs.
 * No tracing, video, HAR, raw headers/auth responses, or on-disk storageState.
 * Node's runner avoids Playwright Test's automatic unredacted error contexts.
 * Reports/screenshots go ONLY beneath the chosen runtime/evidence directory.
 * GL_EVIDENCE_DIR may name a NEW folder below it, with an existing owned parent.
 *
 * Approval gates (off by default):
 *   GL_RUN_QUIZ=1                real generation + server grading + reload;
 *   GL_QUIZ_ACTOR=learner_b      optional quiz actor; also needs clear approval;
 *   GL_ALLOW_LEARNER_B_CLEAR=1   enable/clear ONLY learner_b after export/shot;
 *   GL_EXISTING_QUIZ_ID=<uuid>  reload a real prior quiz (including stale);
 *   GL_REPLAY_QUIZ=1           verify that ready quiz without generating again;
 *   GL_CREATE_CARD=1           create a restricted disposable Agent/chat card;
 *   GL_CARD_SESSION_ID=<uuid>   existing real learner_a learning-tool chat;
 *   GL_RUN_LEARNER_B_CHECKS=1   only after coordinating with the HTTP-test owner;
 *   GL_RUN_MOCK_ERRORS=1        separately labelled HTTP error injection only.
 * GL_EXPECT_PROMPT_VERSION may pin a parent-approved backend prompt version.
 * GL_QUIZ_TIMEOUT_MS (default 180000, max 300000) bounds real model waiting.
 * No SQL, lifecycle commands, synthetic success DTOs, forced clicks, or mastery
 * manipulation. Reading/answers are automation observations, NOT human gains.
 * The default incomplete-evidence case permits ONE real prepare request only
 * while fixture sources/citations are incomplete; if accepted it stops UI polling and
 * never answers. No pending job is forced, deleted, or marked complete.
 */
import test, { before, after } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createRequire } from 'node:module';
import { createHash } from 'node:crypto';
import { FIXTURE_SLUGS } from './guided-learning-config.mjs';
import { createEvidenceDirectory, loadFixtureRuntime, readFixtureManifest } from './guided-learning-runtime.mjs';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
let API, FRONTEND, EVIDENCE, runtimePaths;
const RUN_QUIZ = process.env.GL_RUN_QUIZ === '1';
const CLEAR_B = process.env.GL_ALLOW_LEARNER_B_CLEAR === '1';
const QUIZ_ACTOR = process.env.GL_QUIZ_ACTOR || 'learner_a';
assert.ok(['learner_a', 'learner_b'].includes(QUIZ_ACTOR), 'Only seeded quiz actors are allowed');
assert.ok(QUIZ_ACTOR !== 'learner_b' || CLEAR_B, 'learner_b quiz opt-in requires explicit disposable-profile approval');
const CHECK_B = process.env.GL_RUN_LEARNER_B_CHECKS === '1';
const MOCK_ERRORS = process.env.GL_RUN_MOCK_ERRORS === '1';
const CREATE_CARD = process.env.GL_CREATE_CARD === '1';
assert.ok(!CREATE_CARD || RUN_QUIZ, 'GL_CREATE_CARD requires GL_RUN_QUIZ=1 (create card before answers)');
const EXISTING_QUIZ = process.env.GL_EXISTING_QUIZ_ID || '';
assert.ok(!EXISTING_QUIZ || /^[0-9a-f-]{36}$/i.test(EXISTING_QUIZ), 'Existing quiz must be a real UUID');
const REPLAY_QUIZ = process.env.GL_REPLAY_QUIZ === '1';
assert.ok(!REPLAY_QUIZ || (RUN_QUIZ && EXISTING_QUIZ && !CREATE_CARD), 'Replay needs GL_RUN_QUIZ + GL_EXISTING_QUIZ_ID, and no new card generation');
const EXPECTED_PROMPT_VERSION = process.env.GL_EXPECT_PROMPT_VERSION || '';
const CARD_SESSION = process.env.GL_CARD_SESSION_ID || '';
const DESKTOP = { width: 1280, height: 900 };
const MOBILE = { width: 390, height: 844 };
const SLUGS = FIXTURE_SLUGS;
const TITLES = ['Retrieval and Evidence', 'Assessment and Feedback', 'Review and Prerequisites'];
const REASONS = { practice: 'Needs practice', source_changed: 'Sources changed', source_backed: 'Backed by sources', explore: 'Explore a new topic', review_due: 'Review due', review_need: 'Needs review', graph_frontier: 'Related topic', interest_match: 'Matches your interests', content_quality: 'Source quality', cold_start: 'Starting topic', exploration: 'Explore a new topic', related_topic: 'Related topic' };
const COLORS = { unseen: '#8c8c8c', learning: '#0052d9', mastered: '#2ba471', review_due: '#e37318' };
const require = createRequire(import.meta.url);
let playwright, browser, seed, manifest;
const secrets = new Set();
const reports = [];
let sourceAtStart;
let runBrowserVersion;
let fixtureHashAtStart;
let generatedCardSession = '';
let generatedCardQuiz = '';
let generatedCardResults;

function rememberSecrets(value) {
  if (!value || typeof value !== 'object') return;
  for (const [key, item] of Object.entries(value)) {
    if (/password|token|email|username|api.?key/i.test(key) && typeof item === 'string' && item) secrets.add(item);
    else if (typeof item === 'object') rememberSecrets(item);
  }
}
function redact(value) {
  let text = String(value);
  for (const secret of [...secrets].sort((a, b) => b.length - a.length)) text = text.split(secret).join('[REDACTED]');
  return text.replace(/\x1b\[[0-9;]*m/g, '').replace(/Bearer\s+\S+/gi, 'Bearer [REDACTED]').replace(/eyJ[\w-]+\.[\w-]+\.[\w-]+/g, '[REDACTED JWT]');
}
function safeURL(raw) {
  try { const u = new URL(raw); return u.pathname; } catch { return '[invalid URL]'; }
}
async function writeJSON(file, value) {
  await fs.writeFile(path.join(EVIDENCE, file), redact(JSON.stringify(value, null, 2)) + '\n', { mode: 0o600 });
}
async function sourceHashes() {
  const files = ['src/views/knowledge/wiki/LearningPanel.vue', 'src/views/knowledge/wiki/LearningQuiz.vue', 'src/views/knowledge/wiki/WikiBrowser.vue', 'src/views/knowledge/KnowledgeBase.vue', 'src/composables/useLearningState.ts', 'src/i18n/locales/en-US.ts'];
  return Object.fromEntries(await Promise.all(files.map(async file => [file, createHash('sha256').update(await fs.readFile(path.join(ROOT, 'frontend', file))).digest('hex')])));
}

before(async () => {
  const runtime = await loadFixtureRuntime(ROOT, process.env,
    `guided-learning-${new Date().toISOString().replace(/[:.]/g, '-')}-${process.pid}`);
  ({ apiBase: API, frontendBase: FRONTEND, state: seed, fixtures: manifest, paths: runtimePaths } = runtime);
  EVIDENCE = runtimePaths.evidenceDir;
  rememberSecrets(seed);
  rememberSecrets(manifest);
  fixtureHashAtStart = createHash('sha256').update(runtime.manifestText).digest('hex');
  try { playwright = require(path.join(ROOT, 'frontend/node_modules/playwright')); }
  catch { throw new Error('Install the pinned local Playwright with npm ci in frontend'); }
  assert.ok(playwright?.chromium && playwright?.request, 'Local Playwright installation is incomplete');
  await createEvidenceDirectory(runtimePaths);
  sourceAtStart = await sourceHashes();
  // Some development sandboxes disallow child-process OOM priority writes.
  // Single-process mode avoids those writes; this is a UI check, not a process-isolation test.
  const args = ['--disable-gpu', ...(process.env.GL_SINGLE_PROCESS === '1' ? ['--single-process', '--no-zygote'] : [])];
  browser = await playwright.chromium.launch({ headless: true, args });
  runBrowserVersion = browser.version();
});

after(async () => {
  await browser?.close();
  if (!sourceAtStart) return;
  const sourceAtEnd = await sourceHashes();
  await writeJSON('summary.json', {
    startedSources: sourceAtStart, finishedSources: sourceAtEnd,
    sourceChangedDuringRun: JSON.stringify(sourceAtStart) !== JSON.stringify(sourceAtEnd),
    fixtureHashAtStart, fixtureHashAtEnd: createHash('sha256').update(await readFixtureManifest(runtimePaths)).digest('hex'),
    fixtures: Object.fromEntries(Object.entries(manifest).map(([label, fixture]) => [label, { tenant_id: fixture.tenant_id, knowledge_base_id: fixture.knowledge_base_id, pages: fixture.pages }])),
    specSha256AtReport: createHash('sha256').update(await fs.readFile(fileURLToPath(import.meta.url))).digest('hex'),
    browser: runBrowserVersion, frontend: FRONTEND, api: API,
    singleProcess: process.env.GL_SINGLE_PROCESS === '1',
    approvals: { realQuiz: RUN_QUIZ, quizActor: QUIZ_ACTOR, replayQuiz: REPLAY_QUIZ, clearLearnerB: CLEAR_B, readOnlyLearnerBChecks: CHECK_B, cardSessionSupplied: !!CARD_SESSION, createRealCard: CREATE_CARD, existingQuizSupplied: !!EXISTING_QUIZ, mockErrors: MOCK_ERRORS },
    limits: ['No latency/P95 measurement', 'No human learning-gain claim', 'No credentials, traces, HAR, videos or persisted login state', 'Quiz/card/clear gates are not passes when skipped'],
    gatedCoverage: [
      { name: 'real quiz/answer/reload', enabled: RUN_QUIZ },
      { name: 'real tool card', enabled: !!CARD_SESSION || CREATE_CARD },
      { name: 'physical learner_b clear', enabled: CLEAR_B },
      { name: 'learner_b browser privacy checks (coordinate first)', enabled: CHECK_B },
      { name: 'mock error branch (not successful E2E)', enabled: MOCK_ERRORS },
    ],
    results: reports,
  });
  const lines = ['# Guided learning E2E', '', `Browser: Chromium ${runBrowserVersion}`, `Source changed during run: ${JSON.stringify(sourceAtStart) !== JSON.stringify(sourceAtEnd)}`, '', ...reports.map(r => `- ${r.outcome.toUpperCase()}: ${r.name}${r.failures.length ? ' — ' + r.failures.join('; ') : ''}`), '', 'See per-case JSON for snapshots, network/console diagnostics and bounding-box checks. Screenshots are viewport-sized with identity masking. No performance or human-learning claim.'];
  await fs.writeFile(path.join(EVIDENCE, 'report.md'), redact(lines.join('\n')) + '\n');
  console.log('Sanitized evidence: ' + path.relative(ROOT, EVIDENCE));
});

async function apiCall(app, method, endpoint, body) {
  // Request/response bodies are deliberately never logged, including failures.
  let response;
  try { response = await app.api.fetch('/api/v1' + endpoint, { method, data: body, maxRedirects: 0, timeout: 20000 }); }
  catch { throw new Error(`API transport failed: ${method} ${endpoint.split('?')[0]}`); }
  let payload;
  try { payload = await response.json(); } catch { payload = {}; }
  return { status: response.status(), code: payload.error?.code, data: payload.data ?? payload, ok: response.ok() && payload.success !== false };
}
async function data(app, endpoint) {
  const r = await apiCall(app, 'GET', endpoint);
  assert.ok(r.ok, `GET ${endpoint.split('?')[0]} failed (${r.status}, ${r.code || 'unknown'})`);
  return r.data;
}
function check(app, condition, message) {
  app.report.checks.push({ passed: !!condition, message });
  if (!condition) app.report.failures.push(message);
}
async function snapshot(app, label) {
  // Playwright equivalents of tab-list + accessibility snapshot, before actions.
  const selectors = ['.learning-panel', '.wiki-reader-header', '.wiki-graph-legend', '.t-dialog:visible', '.agent-stream-display .tree-root', '.agent-stream-display .action-header'];
  const trees = [];
  for (const selector of selectors) for (const node of await app.page.locator(selector).all()) {
    trees.push({ selector, tree: redact(await node.ariaSnapshot()) });
  }
  app.report.snapshots.push({ label, tabs: app.context.pages().map(p => safeURL(p.url())), trees });
}
async function click(app, locator, label) {
  await snapshot(app, 'before-' + label);
  await locator.scrollIntoViewIfNeeded({ timeout: 10000 });
  await locator.click({ timeout: 10000 });
}
async function waitUntil(app, predicate, label, timeout = 15000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) {
    if (await predicate()) return;
    await app.page.waitForTimeout(1000);
    await snapshot(app, 'waiting-' + label);
  }
  throw new Error(`Timed out: ${label}`);
}
async function shot(app, label) {
  await snapshot(app, label);
  const text = await app.page.locator('body').innerText();
  const user = seed.users[app.label];
  for (const secret of [user.password, user.token, app.token]) {
    assert.ok(!secret || !text.includes(secret), 'Refusing screenshot with exposed secret');
  }
  const mask = [app.page.locator('.user-menu'), app.page.locator('[data-guide="user-menu"]'), app.page.locator('input[type="password"],input[type="email"]')];
  for (const account of Object.values(seed.users)) for (const key of ['username', 'email']) if (account[key]) mask.push(app.page.getByText(account[key], { exact: false }));
  const filename = `${app.report.name}-${label}.png`;
  await app.page.screenshot({ path: path.join(EVIDENCE, filename), fullPage: false, animations: 'disabled', mask, maskColor: '#737373' });
  app.report.screenshots.push(filename);
}
async function layout(app, label) {
  const geometry = await app.page.evaluate(() => {
    const rect = el => { const r = el.getBoundingClientRect(); return { x: r.x, y: r.y, width: r.width, height: r.height, right: r.right, bottom: r.bottom }; };
    const selectors = ['.learning-panel', '.learning-toolbar', '.learning-content', '.learning-overview', '.learning-practice', '.wiki-browser', '.wiki-sidebar', '.wiki-content', '.wiki-reader-title-row', '.wiki-reader-title-block', '.wiki-reader-aside', '.wiki-graph', '.wiki-graph-search-container', '.wiki-graph-legend', '.t-dialog'];
    const boxes = {};
    for (const selector of selectors) {
      const el = document.querySelector(selector);
      if (el && el.getClientRects().length && getComputedStyle(el).visibility !== 'hidden') boxes[selector] = { ...rect(el), clientWidth: el.clientWidth, scrollWidth: el.scrollWidth };
    }
    const visibleRect = el => {
      const raw = rect(el); const visible = { ...raw };
      for (let parent = el.parentElement; parent; parent = parent.parentElement) {
        const clip = parent.getBoundingClientRect(); const css = getComputedStyle(parent);
        if (/(auto|scroll|hidden|clip)/.test(css.overflowX)) { visible.x = Math.max(visible.x, clip.left); visible.right = Math.min(visible.right, clip.right); }
        if (/(auto|scroll|hidden|clip)/.test(css.overflowY)) { visible.y = Math.max(visible.y, clip.top); visible.bottom = Math.min(visible.bottom, clip.bottom); }
      }
      visible.width = visible.right - visible.x; visible.height = visible.bottom - visible.y;
      return { ...visible, raw };
    };
    // Scrolled-out quiz choices are not overlapping toolbar controls: clip
    // geometry against each overflow ancestor instead of counting hidden boxes.
    const controls = [...document.querySelectorAll('.learning-toolbar button,.learning-toolbar .t-switch,.quiz-option,.quiz-status button')]
      .filter(el => el.getClientRects().length).map(el => ({ tag: el.tagName, label: el.getAttribute('aria-label') || el.textContent.trim(), ...visibleRect(el) }))
      .filter(box => box.width > 1 && box.height > 1);
    const overlap = (a, b) => a && b && Math.min(a.right, b.right) - Math.max(a.x, b.x) > 1 && Math.min(a.bottom, b.bottom) - Math.max(a.y, b.y) > 1;
    const overlaps = [];
    for (let i = 0; i < controls.length; i++) for (let j = i + 1; j < controls.length; j++) if (overlap(controls[i], controls[j])) overlaps.push([controls[i].label, controls[j].label]);
    for (const [a, b] of [['.learning-panel', '.wiki-browser'], ['.learning-overview', '.learning-practice'], ['.wiki-graph-search-container', '.wiki-graph-legend'], ['.wiki-reader-title-block', '.wiki-reader-aside']]) if (overlap(boxes[a], boxes[b])) overlaps.push([a, b]);
    return { viewport: { width: innerWidth, height: innerHeight }, documentWidth: document.documentElement.scrollWidth, boxes, controls, overlaps };
  });
  app.report.layouts.push({ label, ...geometry });
  const expectedWidth = app.page.viewportSize().width;
  check(app, geometry.documentWidth <= expectedWidth + 1, `${label}: no document horizontal overflow (actual ${geometry.documentWidth}/${expectedWidth})`);
  check(app, geometry.viewport.width <= expectedWidth + 1, `${label}: mobile layout viewport does not widen beyond requested width`);
  for (const selector of ['.learning-panel', '.wiki-graph-search-container', '.wiki-graph-legend', '.t-dialog']) {
    const r = geometry.boxes[selector];
    if (r) check(app, r.x >= -1 && r.right <= expectedWidth + 1, `${label}: ${selector} stays inside viewport`);
  }
  const content = geometry.boxes['.learning-content'];
  if (content) check(app, content.scrollWidth <= content.clientWidth + 1, `${label}: learning content has no horizontal overflow`);
  check(app, geometry.overlaps.length === 0, `${label}: controls/columns/graph overlays do not overlap${geometry.overlaps.length ? ' ' + JSON.stringify(geometry.overlaps) : ''}`);
}
async function openWiki(app, slug = SLUGS[0], tab = 'wiki') {
  await snapshot(app, 'before-navigation');
  await app.page.goto(`${FRONTEND}/platform/knowledge-bases/${app.fixture.knowledge_base_id}?${new URLSearchParams({ tab, ...(slug ? { slug } : {}) })}`, { waitUntil: 'domcontentloaded', timeout: 30000 });
  await app.page.locator('.learning-panel').waitFor({ timeout: 30000 });
  if (slug && tab === 'wiki') {
    const target = await data(app, `/knowledgebase/${app.fixture.knowledge_base_id}/wiki/pages/${slug}`);
    await waitUntil(app, async () => (await app.page.locator('.wiki-reader-title').textContent())?.trim() === target.title, 'requested fixture page rendered');
  }
  await waitUntil(app, async () => !(await app.page.locator('.learning-toolbar .t-switch').getAttribute('class') || '').includes('t-is-loading'), 'settings ready');
  await snapshot(app, 'loaded');
}
async function ensureEnabled(app) {
  const settings = await data(app, '/learning/settings');
  if (!settings.enabled) {
    assert.ok(app.label === 'learner_a' || CLEAR_B, 'learner_b opt-in needs destructive-test approval');
    await shot(app, 'opt-out');
    const response = app.page.waitForResponse(r => safeURL(r.url()) === '/api/v1/learning/settings' && r.request().method() === 'PUT');
    await click(app, app.page.locator('.learning-toolbar [aria-label="Personal learning history"]'), 'opt-in');
    const r = await response;
    assert.equal(r.status(), 200, 'Opt-in must succeed through actual API');
  } else app.report.notes.push('Already opted in: preserved history; did not reset consent to manufacture a cold start.');
  await app.page.locator('.learning-content').waitFor();
  await waitUntil(app, async () => (await app.page.locator('.overview-counts strong').count()) === 4, 'overview counts');
  check(app, (await data(app, '/learning/settings')).enabled === true, 'Opt-in persisted on backend');
}
async function collapseMobileSidebar(app) {
  const collapse = app.page.locator('.sidebar-toggle[title="Collapse Sidebar"]');
  if (await collapse.count()) {
    await click(app, collapse, 'collapse-sidebar');
    await app.page.locator('.aside_box--collapsed').waitFor();
    // CSS width transition: incremental observation, not a blind long sleep.
    await app.page.waitForTimeout(1000);
    await snapshot(app, 'sidebar-collapsed');
  }
}
async function fixtureSources(app) {
  const docs = [];
  for (const slug of SLUGS) {
    const doc = await data(app, '/knowledge/' + app.fixture.documents[slug]);
    const page = await data(app, `/knowledgebase/${app.fixture.knowledge_base_id}/wiki/pages/${slug}`);
    check(app, page.id === app.fixture.pages[slug].page_id && page.status === 'published', `Published fixture identity: ${slug}`);
    const chunks = await data(app, '/chunks/' + app.fixture.documents[slug] + '?page=1&page_size=100');
    docs.push({ slug, parse_status: doc.parse_status, enable_status: doc.enable_status, chunk_refs: page.chunk_refs || [], ready_chunks: (Array.isArray(chunks) ? chunks : []).filter(c => c.index_status === 'ready' && c.is_enabled !== false && c.content).map(c => c.id) });
  }
  app.report.sourceStatus = docs;
  return docs;
}
async function exportUI(app, suffix = 'export', all = false) {
  await click(app, app.page.getByRole('button', { name: 'Learning data', exact: true }), 'privacy-menu');
  await shot(app, suffix + '-menu');
  const downloadPromise = app.page.waitForEvent('download', { timeout: 20000 }).catch(() => null);
  const responsePromise = app.page.waitForResponse(r => safeURL(r.url()) === '/api/v1/learning/export' && r.request().method() === 'GET');
  await click(app, app.page.getByText(all ? 'Export all learning data (JSON)' : 'Export this knowledge base (JSON)', { exact: true }), 'export');
  const response = await responsePromise;
  if (!response.ok()) {
    await app.page.locator('.learning-panel > .panel-message[role="alert"]').waitFor();
    await shot(app, suffix + '-http-' + response.status());
    throw new Error(`Actual export rejected with HTTP ${response.status()}; no download claimed`);
  }
  const download = await downloadPromise;
  assert.ok(download, 'Expected browser export download was not emitted');
  const stream = await download.createReadStream();
  assert.ok(stream, 'Export download stream unavailable');
  const buffers = [];
  for await (const chunk of stream) buffers.push(chunk);
  const raw = Buffer.concat(buffers).toString('utf8');
  assert.equal(raw, redact(raw), 'Export must not contain credentials');
  const document = JSON.parse(raw);
  for (const collection of ['nodes', 'quizzes', 'attempts']) {
    for (const item of document[collection] || []) check(app, item.knowledge_base_id === app.fixture.knowledge_base_id, `Export ${collection} stays in fixture KB`);
  }
  check(app, !!document.settings && !!document.exported_at, 'Export has settings and timestamp');
  await writeJSON(`${app.report.name}-${suffix}.json`, document);
  await download.delete();
  app.report.exportRetained = true;
  await app.page.keyboard.press('Escape');
  await app.page.getByText('Clear this knowledge base', { exact: true }).waitFor({ state: 'hidden' });
  return document;
}

async function scenario(name, label, viewport, callback) {
  const report = { name, actor: label, viewport, outcome: 'running', failures: [], checks: [], notes: [], snapshots: [], layouts: [], screenshots: [], network: [], console: [], pageErrors: [], blockedRequests: [] };
  reports.push(report);
  let app;
  let loginAPI;
  try {
    loginAPI = await playwright.request.newContext({ baseURL: API, timeout: 20000 });
    const account = seed.users[label];
    let response;
    try { response = await loginAPI.post('/api/v1/auth/login', { data: { email: account.email, password: account.password }, maxRedirects: 0 }); }
    catch { throw new Error('API login transport failed (details suppressed)'); }
    assert.equal(response.status(), 200, 'API login rejected (response suppressed)');
    const payload = await response.json();
    const auth = payload.data || payload;
    rememberSecrets(auth);
    const tenant = auth.active_tenant || auth.tenant;
    assert.ok(auth.token && auth.user?.id === account.user_id && tenant?.id === manifest[label].tenant_id, 'Login scope does not match seed');
    const storage = { locale: 'en-US', weknora_token: auth.token, weknora_user: JSON.stringify({ ...auth.user, tenant_id: String(auth.user.tenant_id) }), weknora_tenant: JSON.stringify({ ...tenant, id: String(tenant.id) }), weknora_memberships: JSON.stringify(auth.memberships || []), 'weknora:new-user-guide-done:v1': '1', 'weknora:contextual-guide-kb-detail:v1': '1' };
    if (auth.refresh_token) storage.weknora_refresh_token = auth.refresh_token;
    const api = await playwright.request.newContext({ baseURL: API, extraHTTPHeaders: { Authorization: `Bearer ${auth.token}` }, timeout: 20000 });
    if (process.env.GL_SINGLE_PROCESS === '1' && !browser.isConnected()) {
      browser = await playwright.chromium.launch({ headless: true, args: ['--disable-gpu', '--single-process', '--no-zygote'] });
    }
    const context = await browser.newContext({ viewport, locale: 'en-US', deviceScaleFactor: 1, isMobile: viewport.width < 500, hasTouch: viewport.width < 500, acceptDownloads: true, storageState: { cookies: [], origins: [{ origin: FRONTEND, localStorage: Object.entries(storage).map(([name, value]) => ({ name, value })) }] } });
    const page = await context.newPage();
    page.setDefaultTimeout(12000);
    app = { label, report, context, page, api, token: auth.token, fixture: manifest[label], allowPrepare: false, allowAnswers: false, allowDelete: false, expectedErrors: [], mock: null };
    // Safety interceptor only: continue real traffic; NEVER fabricate success.
    await context.route('**/*', async route => {
      const req = route.request();
      const url = new URL(req.url());
      const method = req.method();
      if (url.origin !== FRONTEND) { report.blockedRequests.push({ method, path: url.pathname, reason: 'foreign origin' }); return route.abort('blockedbyclient'); }
      if (app.mock && app.mock.path === url.pathname) return app.mock.handler(route);
      if (['GET', 'HEAD', 'OPTIONS'].includes(method)) return route.continue();
      const body = req.postDataJSON() || {};
      const pageIDs = SLUGS.map(s => app.fixture.pages[s].page_id);
      const viewMatch = method === 'POST' && url.pathname.match(/^\/api\/v1\/learning\/nodes\/([0-9a-f-]+)\/view$/i);
      if (viewMatch && !pageIDs.includes(viewMatch[1])) {
        // Wiki enrichment can auto-select a derived topic during initial load.
        // Admit only a real server node in this same disposable fixture KB.
        const node = await apiCall(app, 'GET', '/learning/nodes/' + viewMatch[1]);
        if (node.ok && node.data.knowledge_base_id === app.fixture.knowledge_base_id) {
          report.notes.push('Allowed transient view of an API-verified derived topic in the owned fixture KB.');
          return route.continue();
        }
      }
      const allowed =
        (url.pathname === '/api/v1/learning/settings' && method === 'PUT' && (label === 'learner_a' ? body.enabled === true : CLEAR_B)) ||
        (url.pathname === '/api/v1/learning/overlay' && method === 'POST' && body.knowledge_base_id === app.fixture.knowledge_base_id) ||
        (method === 'POST' && pageIDs.some(id => url.pathname === `/api/v1/learning/nodes/${id}/view`)) ||
        (app.allowPrepare && method === 'POST' && url.pathname === '/api/v1/learning/question-sets' && pageIDs.includes(body.page_id)) ||
        (app.allowAnswers && RUN_QUIZ && method === 'POST' && url.pathname === '/api/v1/learning/attempts') ||
        (app.allowDelete && CLEAR_B && label === 'learner_b' && method === 'DELETE' && url.pathname === '/api/v1/learning/profile' && (!url.search || url.searchParams.get('knowledge_base_id') === app.fixture.knowledge_base_id)) ||
        (method === 'POST' && url.pathname === '/api/v1/auth/refresh');
      if (!allowed) { report.blockedRequests.push({ method, path: url.pathname, reason: 'mutation outside approved fixture operations' }); return route.abort('blockedbyclient'); }
      return route.continue();
    });
    page.on('response', r => { if (r.url().includes('/api/')) report.network.push({ method: r.request().method(), path: safeURL(r.url()), status: r.status() }); });
    page.on('requestfailed', r => { report.network.push({ method: r.method(), path: safeURL(r.url()), failure: r.failure()?.errorText }); });
    page.on('pageerror', e => report.pageErrors.push(redact(e.message)));
    page.on('console', m => { if (['error', 'warning'].includes(m.type())) report.console.push({ level: m.type(), text: redact(m.text()), path: safeURL(m.location().url || ''), line: m.location().lineNumber }); });
    await callback(app);
  } catch (error) {
    report.failures.push(redact(error.message || error.name));
    if (app) { try { await shot(app, 'failure'); } catch { report.notes.push('Failure screenshot unavailable or refused by secret guard.'); } }
  } finally {
    if (app) {
      check(app, report.pageErrors.length === 0, 'No unhandled browser page errors');
      const unexpected = report.network.filter(r => r.status >= 400 && !app.expectedErrors.some(e => e.path === r.path && e.status === r.status));
      check(app, unexpected.length === 0, 'No unexpected HTTP errors' + (unexpected.length ? ': ' + JSON.stringify(unexpected) : ''));
      const consoleErrors = report.console.filter(m => m.level === 'error' && !(m.text.startsWith('Failed to load resource:') && app.expectedErrors.some(e => e.path === m.path && m.text.includes(String(e.status)))));
      check(app, consoleErrors.length === 0, 'No unexpected console errors');
      check(app, report.blockedRequests.length === 0, 'No out-of-scope requests');
      // Chromium single-process mode cannot destroy/recreate renderer contexts.
      // Close the entire owned browser cleanly, then launch anew for the next scenario.
      if (process.env.GL_SINGLE_PROCESS === '1') await browser.close();
      else await app.context.close();
      await app.api.dispose();
    }
    await loginAPI?.dispose();
    report.outcome = report.failures.length ? 'failed' : report.skipped ? 'skipped' : 'passed';
    await writeJSON(`${name}.json`, report);
  }
  assert.equal(report.failures.length, 0, redact(`${name}: ${report.failures.join('; ')}`));
}

test('desktop: opt-in, recommendation reasons, three pages, familiar is not mastery', async () => {
  await scenario('desktop-learning', 'learner_a', DESKTOP, async app => {
    await openWiki(app);
    await ensureEnabled(app);
    await fixtureSources(app);
    const beforeNodes = await Promise.all(SLUGS.map(slug => data(app, '/learning/nodes/' + app.fixture.pages[slug].page_id)));
    const refreshedRecommendations = app.page.waitForResponse(r => safeURL(r.url()) === '/api/v1/learning/recommendations' && r.request().method() === 'GET');
    await click(app, app.page.locator('.overview-heading').getByRole('button', { name: 'Refresh', exact: true }), 'refresh-recommendations');
    const recommendations = (await (await refreshedRecommendations).json()).data;
    await waitUntil(app, async () => (await app.page.locator('.learning-recommendations li').count()) === recommendations.length, 'recommendations rendered');
    check(app, recommendations.length > 0, 'Real recommendations are nonempty');
    for (const item of recommendations) {
      const card = app.page.locator('.learning-recommendations li').filter({ has: app.page.getByRole('button', { name: item.title, exact: true }) });
      await waitUntil(app, async () => await card.count() === 1, 'recommendation ' + item.title);
      check(app, await card.count() === 1, 'Recommendation exists: ' + item.title);
      for (const reason of item.reason_codes) if (REASONS[reason]) check(app, (await card.innerText()).includes(REASONS[reason]), 'Server reason rendered: ' + reason);
    }
    const renderedReasons = (await app.page.locator('.learning-recommendations .reasons span').allTextContents()).map(s => s.trim());
    const unknownReasons = [...new Set(recommendations.flatMap(r => r.reason_codes))].filter(code => renderedReasons.includes(code));
    check(app, unknownReasons.length === 0, 'Recommendation reasons have user-facing translations' + (unknownReasons.length ? ': ' + unknownReasons.join(', ') : ''));
    await shot(app, 'opt-in-overview');
    await layout(app, 'desktop-overview');
    const first = recommendations.find(r => SLUGS.includes(r.slug));
    assert.ok(first, 'No curated recommendation available for safe navigation');
    await click(app, app.page.locator('.learning-recommendations').getByRole('button', { name: first.title, exact: true }), 'recommendation');
    await waitUntil(app, async () => (await app.page.locator('.wiki-reader-title').innerText()).trim() === first.title, 'recommendation navigation');
    for (let index = 0; index < SLUGS.length; index++) {
      await snapshot(app, 'before-page-search-' + index);
      await app.page.locator('.wiki-sidebar input').fill(TITLES[index]);
      const match = app.page.locator('.wiki-page-item').filter({ has: app.page.getByText(TITLES[index], { exact: true }) });
      await waitUntil(app, async () => await match.count() === 1, 'curated page search result');
      await click(app, match, 'page-' + index);
      await waitUntil(app, async () => (await app.page.locator('.wiki-reader-title').innerText()).trim() === TITLES[index], 'page heading');
      await waitUntil(app, async () => !!(await data(app, '/learning/nodes/' + app.fixture.pages[SLUGS[index]].page_id)).familiar, 'familiar persisted');
      const node = await data(app, '/learning/nodes/' + app.fixture.pages[SLUGS[index]].page_id);
      check(app, node.mastery.attempts === beforeNodes[index].mastery.attempts && node.mastery.p_mastery === beforeNodes[index].mastery.p_mastery && node.mastery.state === beforeNodes[index].mastery.state, 'Reading gives no mastery credit: ' + SLUGS[index]);
      await shot(app, 'page-' + index);
    }
    const badge = app.page.locator('.page-state .learning-badge.familiar');
    await snapshot(app, 'before-familiar-tooltip');
    await badge.scrollIntoViewIfNeeded(); await badge.hover();
    await app.page.getByText('Reading and source use indicate familiarity, not mastery.', { exact: true }).waitFor();
    await shot(app, 'familiar-tooltip');
    await snapshot(app, 'before-reload'); await app.page.reload({ waitUntil: 'domcontentloaded' });
    await app.page.locator('.page-state .learning-badge.familiar').waitFor();
    await shot(app, 'persisted-reload');
    const other = seed.users.learner_b;
    check(app, other.tenant_id !== app.fixture.tenant_id, 'Other disposable learner stays in a separate tenant');
    const consent = app.page.locator('.learning-toolbar [aria-label="Personal learning history"]');
    const a11y = await consent.evaluate(el => ({ tag: el.tagName, role: el.getAttribute('role'), tabindex: el.getAttribute('tabindex'), checked: el.getAttribute('aria-checked') }));
    app.report.consentAccessibility = a11y;
    check(app, ['switch', 'checkbox'].includes(a11y.role) && a11y.checked !== null, 'Consent exposes switch/checkbox role and checked state to assistive technology');
  });
});

test('mobile: default and collapsed navigation, recommendations and page layout', async () => {
  await scenario('mobile-learning', 'learner_a', MOBILE, async app => {
    await openWiki(app);
    await ensureEnabled(app);
    await shot(app, 'default-navigation'); await layout(app, 'mobile-default');
    await collapseMobileSidebar(app);
    await shot(app, 'collapsed-navigation'); await layout(app, 'mobile-collapsed');
    await click(app, app.page.locator('.learning-recommendations').getByRole('button', { name: TITLES[1], exact: true }), 'recommendation');
    await waitUntil(app, async () => (await app.page.locator('.wiki-reader-title').innerText()).trim() === TITLES[1], 'mobile page navigation');
    await app.page.locator('.learning-practice').scrollIntoViewIfNeeded();
    await shot(app, 'practice'); await layout(app, 'mobile-practice');
    await click(app, app.page.getByRole('button', { name: 'Guided learning', exact: true }), 'collapse-learning');
    await app.page.locator('.wiki-reader-title').scrollIntoViewIfNeeded();
    await shot(app, 'reader'); await layout(app, 'mobile-reader');
  });
});

for (const [name, viewport] of [['desktop', DESKTOP], ['mobile', MOBILE]]) test(`${name}: graph legend, familiar ring and assessed-state square`, async () => {
  await scenario(name + '-graph', 'learner_a', viewport, async app => {
    await openWiki(app, '', 'graph'); await ensureEnabled(app);
    if (name === 'mobile') await collapseMobileSidebar(app);
    await app.page.locator('.learning-legend').waitFor();
    await waitUntil(app, async () => (await app.page.locator('.node-learning-state').count()) >= 3, 'graph markers');
    // Let the actual overlay request populate accessible SVG titles.
    await waitUntil(app, async () => (await app.page.locator('.wiki-graph-canvas title').allTextContents()).some(t => t.includes('assessed answers')), 'graph overlay');
    const overlay = await apiCall(app, 'POST', '/learning/overlay', { knowledge_base_id: app.fixture.knowledge_base_id, slugs: SLUGS });
    assert.ok(overlay.ok, 'Actual overlay API failed');
    const markers = await app.page.locator('.node-learning-state').evaluateAll(els => els.map(el => ({ title: el.parentElement.querySelector('title')?.textContent, color: el.getAttribute('fill'), display: el.style.display, familiarOpacity: el.parentElement.querySelector('.node-familiar-ring')?.style.opacity })));
    app.report.markers = markers;
    for (const node of overlay.data) {
      const marker = markers.find(m => m.title?.split('\n')[0] === node.title);
      check(app, marker?.color === COLORS[node.mastery.state] && marker.display !== 'none', 'Graph square matches real mastery: ' + node.title);
      if (node.familiar) check(app, marker?.familiarOpacity === '0.9' && marker.title.includes('not mastery'), 'Familiar ring remains distinct: ' + node.title);
    }
    check(app, (await app.page.locator('.learning-legend .learning-badge').allTextContents()).map(s => s.trim()).join('|') === 'Unassessed|Learning|Mastered|Review due', 'All four mastery states have text labels');
    await shot(app, 'expanded'); await layout(app, name + '-graph-expanded');
    await click(app, app.page.getByRole('button', { name: 'Guided learning', exact: true }), 'collapse-learning');
    await shot(app, 'legend'); await layout(app, name + '-graph-legend');
  });
});

test('desktop: graph selection remains the active learning page after switching to Wiki', async () => {
  await scenario('desktop-graph-to-wiki', 'learner_a', DESKTOP, async app => {
    await openWiki(app, '', 'graph');
    await ensureEnabled(app);
    await waitUntil(app, async () =>
      (await app.page.locator('.wiki-graph-canvas title').allTextContents())
        .some(title => title.split('\n')[0] === TITLES[1]), 'target graph node');
    await click(app, app.page.locator(`.wiki-graph-canvas g[data-slug="${SLUGS[1]}"]`), 'graph-target');
    await waitUntil(app, async () =>
      new URL(app.page.url()).searchParams.get('slug') === SLUGS[1], 'graph slug route');
    const drawer = app.page.locator('.wiki-graph-drawer');
    await drawer.locator('.t-drawer__header').filter({ hasText: TITLES[1] }).waitFor();
    await snapshot(app, 'before-close-graph-drawer');
    await app.page.keyboard.press('Escape');
    await drawer.waitFor({ state: 'hidden' });
    await click(app, app.page.locator('.breadcrumb-tab').filter({ hasText: 'Wiki' }).first(), 'wiki-tab');
    await waitUntil(app, async () =>
      (await app.page.locator('.wiki-reader-title').innerText()).trim() === TITLES[1], 'matching Wiki reader');
    const route = new URL(app.page.url());
    check(app, route.searchParams.get('tab') === 'wiki' && route.searchParams.get('slug') === SLUGS[1],
      'Graph selection keeps the same slug when entering the Wiki reader');
    check(app, await app.page.locator('.learning-practice').count() === 1,
      'Learning panel remains scoped to the selected page');
    await shot(app, 'selected-page');
  });
});

test('real incomplete evidence: rejection/disabled controls, no answers or forced completion', async t => {
  await scenario('real-evidence-gate', 'learner_a', DESKTOP, async app => {
    await openWiki(app); await ensureEnabled(app);
    const sources = await fixtureSources(app);
    const allReadyChunks = new Set(sources.flatMap(s => s.ready_chunks));
    const ready = sources.every(s => s.parse_status === 'completed' && s.enable_status === 'enabled' && s.chunk_refs.length && s.chunk_refs.every(id => allReadyChunks.has(id)));
    if (ready) {
      app.report.notes.push('Incomplete-evidence branch not applicable: current source references are ready. Use the approved real quiz test.');
      app.report.skipped = true;
      t.skip('Live source references ready; do not manufacture invalid evidence'); return;
    }
    const start = app.page.getByRole('button', { name: 'Practice this page', exact: true });
    await shot(app, 'source-not-ready');
    const disabled = await start.isDisabled();
    check(app, disabled, 'Practice is disabled while source evidence is incomplete or citations are stale');
    if (disabled) return;
    app.allowPrepare = true;
    app.expectedErrors.push({ path: '/api/v1/learning/question-sets', status: 422 });
    const responsePromise = app.page.waitForResponse(r => safeURL(r.url()) === '/api/v1/learning/question-sets' && r.request().method() === 'POST');
    await click(app, start, 'one-real-prepare-probe');
    const response = await responsePromise; app.allowPrepare = false;
    const payload = await response.json();
    app.report.prepare = { status: response.status(), code: payload.error?.code, quizStatus: payload.data?.status, quizId: payload.data?.id };
    if (response.status() === 422) {
      await app.page.getByRole('alert').filter({ hasText: 'Not enough validated source evidence for a quiz.' }).waitFor();
      await shot(app, 'insufficient-evidence');
      check(app, payload.error?.code === 'learning_evidence', 'Real rejection has stable evidence code');
      check(app, await start.isDisabled(), 'Practice remains disabled after known insufficient evidence');
    } else {
      check(app, false, 'Incomplete fixture unexpectedly accepted a quiz; no answer submitted');
      const stop = app.page.getByRole('button', { name: 'Stop waiting', exact: true });
      if (await stop.count()) await click(app, stop, 'stop-polling');
      await shot(app, 'unexpected-accepted-state');
    }
    check(app, await app.page.getByRole('radio').count() === 0, 'No answer controls are exposed without a ready quiz');
    check(app, !app.report.network.some(r => r.path.endsWith('/attempts') && r.method === 'POST'), 'No assessment is fabricated');
  });
});

test('desktop: open an actual cited source from the Wiki reader', async () => {
  await scenario('desktop-source-reading', 'learner_a', DESKTOP, async app => {
    await openWiki(app); await ensureEnabled(app);
    await click(app, app.page.getByRole('button', { name: 'Guided learning', exact: true }), 'collapse-for-source');
    const before = await data(app, '/learning/nodes/' + app.fixture.pages[SLUGS[0]].page_id);
    const source = app.page.locator('.wiki-reader-footer .wiki-reader-footer-row').filter({ hasText: 'Source documents' }).getByRole('link', { name: TITLES[0], exact: true });
    await click(app, source, 'actual-cited-document');
    const drawer = app.page.locator('.doc-main-drawer:visible');
    await waitUntil(app, async () => (await drawer.locator('.doc-drawer-header-title').textContent())?.startsWith(TITLES[0]), 'source document header');
    check(app, app.report.network.some(r => r.path === '/api/v1/knowledge/' + app.fixture.documents[SLUGS[0]] && r.status === 200), 'Source link fetched the actual fixture document');
    await shot(app, 'source-document');
    await click(app, drawer.locator('.t-drawer__close-btn'), 'close-source');
    await waitUntil(app, async () => await app.page.locator('.doc-main-drawer.t-drawer--open').count() === 0, 'source drawer closed');
    const after = await data(app, '/learning/nodes/' + app.fixture.pages[SLUGS[0]].page_id);
    check(app, before.mastery.attempts === after.mastery.attempts && before.mastery.p_mastery === after.mastery.p_mastery, 'Source reading gives no assessment credit');
  });
});

test('real privacy: export learner_a without clearing history', { skip: RUN_QUIZ ? 'Covered by the answered-quiz export; avoid duplicate rate-limited export' : false }, async () => {
  await scenario('desktop-export', 'learner_a', DESKTOP, async app => {
    await openWiki(app); await ensureEnabled(app);
    await shot(app, 'before-export'); await exportUI(app);
  });
});

for (const [device, viewport] of [['desktop', DESKTOP], ['mobile', MOBILE]]) test(`${device}: privacy export/cancel while learner_b is opted out`, { skip: !CHECK_B }, async () => {
  await scenario(device + '-privacy-cancel', 'learner_b', viewport, async app => {
    const before = await data(app, '/learning/settings');
    assert.equal(before.enabled, false, 'learner_b belongs to HTTP tests; refusing to view/alter an enabled profile');
    await openWiki(app);
    if (device === 'mobile') await collapseMobileSidebar(app);
    await shot(app, 'before-privacy'); await layout(app, device + '-before-privacy');
    await exportUI(app, 'opted-out-export', true);
    await click(app, app.page.getByRole('button', { name: 'Learning data', exact: true }), 'privacy-menu');
    await click(app, app.page.getByText('Clear this knowledge base', { exact: true }), 'clear-dialog');
    await app.page.getByText('Permanently delete your practice history, quizzes and mastery for this knowledge base?', { exact: true }).waitFor();
    await shot(app, 'clear-confirmation'); await layout(app, device + '-clear-dialog');
    await click(app, app.page.getByRole('button', { name: 'Cancel', exact: true }), 'cancel-clear');
    check(app, JSON.stringify(await data(app, '/learning/settings')) === JSON.stringify(before), 'learner_b consent is unchanged');
    check(app, !app.report.network.some(r => r.method === 'DELETE'), 'Cancel sends no DELETE');
    await shot(app, 'cancelled');
  });
});

test('two-tenant isolation: learner_a cannot read the other fixture profile', async () => {
  await scenario('tenant-isolation', 'learner_a', DESKTOP, async app => {
    const foreign = await apiCall(app, 'GET', '/learning/nodes/' + manifest.learner_b.pages[SLUGS[0]].page_id);
    check(app, [403, 404].includes(foreign.status), 'Foreign node is denied');
    const exported = await apiCall(app, 'GET', '/learning/export?knowledge_base_id=' + manifest.learner_b.knowledge_base_id);
    const collections = ['nodes', 'quizzes', 'attempts'];
    check(app, [403, 404].includes(exported.status) || (exported.ok && collections.every(k => !(exported.data[k] || []).length)), 'Foreign export is denied or empty');
    app.report.isolation = { foreignNode: { status: foreign.status, code: foreign.code }, foreignExport: { status: exported.status, code: exported.code } };
    app.report.notes.push('Authenticated as enabled learner_a; learner_b credentials are not used. No tenant switching, ownership changes, or foreign profile clearing is attempted.');
  });
});

test('MOCK ERROR ONLY: overview 503 and retry to actual backend', { skip: !MOCK_ERRORS }, async () => {
  await scenario('MOCK-overview-error', 'learner_a', DESKTOP, async app => {
    const endpoint = '/api/v1/learning/overview';
    app.mock = { path: endpoint, handler: route => route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ success: false, error: { code: 'learning_unavailable', message: 'Explicit E2E error injection' } }) }) };
    app.expectedErrors.push({ path: endpoint, status: 503 });
    await openWiki(app);
    const alert = app.page.locator('.learning-panel > .panel-message[role="alert"]');
    await alert.filter({ hasText: 'Learning data could not be loaded.' }).waitFor();
    await shot(app, 'MOCK-503');
    app.mock = null;
    await click(app, alert.getByRole('button', { name: 'Retry', exact: true }), 'retry-real-backend');
    await waitUntil(app, async () => await app.page.locator('.overview-counts strong').count() === 4, 'real overview recovered');
    await shot(app, 'real-recovery');
    app.report.notes.push('Only the failed HTTP response is mocked. Recovery uses the real backend; this is not real successful quiz E2E evidence.');
  });
});

async function readyQuiz(app) {
  if (EXISTING_QUIZ) {
    const old = await data(app, '/learning/question-sets/' + EXISTING_QUIZ);
    assert.ok(old.page_id === app.fixture.pages[SLUGS[0]].page_id && old.knowledge_base_id === app.fixture.knowledge_base_id, 'Existing quiz is not the owned fixture');
    const key = 'weknora.learning.quiz:' + JSON.stringify([String(app.fixture.tenant_id), seed.users[app.label].user_id, app.fixture.knowledge_base_id, old.page_id]);
    await app.context.addInitScript(({ key, id }) => { if (!sessionStorage.getItem(key)) sessionStorage.setItem(key, id); }, { key, id: old.id });
    app.report.notes.push('Reloaded an actual prior quiz through the application scoped sessionStorage pointer; no DTO or success response injected.');
    app.report.previousQuiz = { id: old.id, status: old.status, error_code: old.error_code };
  }
  await openWiki(app); await ensureEnabled(app);
  const sources = await fixtureSources(app);
  assert.ok(sources.every(s => s.parse_status === 'completed' && s.enable_status === 'enabled' && s.chunk_refs.every(id => s.ready_chunks.includes(id))), 'Parent must finish valid sources before approved quiz run');
  if (EXISTING_QUIZ) {
    await waitUntil(app, async () => await app.page.getByRole('button', { name: 'Prepare fresh quiz', exact: true }).count() > 0 || await app.page.locator('.learning-quiz > .questions > li').count() === 3, 'prior quiz loaded');
    await shot(app, 'prior-quiz');
    if (app.report.previousQuiz.status === 'stale') check(app, await app.page.getByRole('radio').count() === 0, 'Stale quiz exposes no answer controls');
  }
  if (REPLAY_QUIZ) {
    const quiz = await data(app, '/learning/question-sets/' + EXISTING_QUIZ);
    assert.ok(quiz.status === 'ready' && quiz.questions.length === 3 && quiz.questions.every(q => q.answered), 'Replay requires a real ready answered quiz; regenerate stale quizzes explicitly');
    app.report.quiz = { id: quiz.id, status: quiz.status, algorithm_version: quiz.algorithm_version, prompt_version: quiz.prompt_version || null, replayOnly: true };
    return quiz;
  }
  app.allowPrepare = true;
  const responsePromise = app.page.waitForResponse(r => safeURL(r.url()) === '/api/v1/learning/question-sets' && r.request().method() === 'POST');
  const prepare = app.page.getByRole('button', { name: /^(Practice this page|Prepare fresh quiz|New practice)$/ }).first();
  await click(app, prepare, 'prepare-approved-real-quiz');
  const response = await responsePromise;
  assert.ok(response.ok(), 'Real quiz preparation rejected; never force readiness');
  const payload = await response.json(); app.allowPrepare = false;
  const quizId = payload.data.id;
  if (['pending', 'running'].includes(payload.data.status)) check(app, await app.page.getByRole('radio').count() === 0, 'Pending real quiz exposes no answer controls');
  await shot(app, 'real-quiz-initial');
  const timeout = Math.min(300000, Number(process.env.GL_QUIZ_TIMEOUT_MS) || 180000);
  await waitUntil(app, async () => {
    if (await app.page.locator('.learning-quiz > .questions > li').count() === 3) return true;
    const error = app.page.locator('.learning-quiz [role="alert"]');
    if (await error.count()) throw new Error('Real quiz error: ' + await error.innerText());
    if (await app.page.getByText('Quiz preparation failed', { exact: true }).count()) throw new Error('Real model quiz failed validation; retained failure, not a success');
    const paused = app.page.getByRole('button', { name: 'Check again', exact: true });
    if (await paused.count()) await click(app, paused, 'resume-real-quiz-status');
    return false;
  }, 'real source-backed quiz ready', timeout);
  const quiz = await data(app, '/learning/question-sets/' + quizId);
  assert.ok(quiz.status === 'ready' && quiz.page_id === app.fixture.pages[SLUGS[0]].page_id && quiz.knowledge_base_id === app.fixture.knowledge_base_id, 'Ready quiz scope mismatch');
  for (const q of quiz.questions) if (!q.answered) check(app, !('result' in q) && !('correct_option' in q) && !('explanation' in q), 'Unanswered DTO hides answer key and explanation');
  app.report.quiz = { id: quiz.id, status: quiz.status, algorithm_version: quiz.algorithm_version, prompt_version: quiz.prompt_version || null };
  if (EXPECTED_PROMPT_VERSION) check(app, quiz.prompt_version === EXPECTED_PROMPT_VERSION, 'Quiz uses parent-approved prompt version');
  return quiz;
}

test('APPROVAL: real quiz, server-graded answers with source quotes, persisted reload', { skip: !RUN_QUIZ }, async () => {
  await scenario('approved-real-quiz', QUIZ_ACTOR, DESKTOP, async app => {
    const quiz = await readyQuiz(app);
    await shot(app, 'ready');
    if (CREATE_CARD) {
      try { await createRealCard(app, quiz); } catch (error) { check(app, false, 'Real card creation failed: ' + redact(error.message)); }
    }
    let submittedThisRun = 0;
    app.allowAnswers = true;
    for (let i = 0; i < quiz.questions.length; i++) {
      const question = quiz.questions[i];
      check(app, question.options.length === 4, 'Real question has four options');
      if (question.answered) { app.report.notes.push('Preserved an existing answered question; did not reset or regrade.'); continue; }
      const item = app.page.locator('.learning-quiz > .questions > li').nth(i);
      const submit = item.getByRole('button', { name: 'Submit answer', exact: true });
      check(app, await submit.isDisabled(), 'Submit requires a selection');
      // Fixed first option deliberately does not consult an answer key or
      // claim correctness/learning. Server grading is the subject of this test.
      await click(app, item.getByRole('radio').first(), 'choose-option-' + i);
      const responsePromise = app.page.waitForResponse(r => safeURL(r.url()) === '/api/v1/learning/attempts' && r.request().method() === 'POST');
      await click(app, submit, 'submit-answer-' + i);
      const response = await responsePromise;
      assert.equal(response.status(), 200, 'Server answer submission failed');
      submittedThisRun++;
      const result = (await response.json()).data;
      check(app, result.question_id === question.id && result.selected_option === question.options[0].id, 'Server result matches selected question/option');
      check(app, !!result.explanation && result.evidence?.length > 0, 'Server result includes explanation and evidence');
      for (const evidence of result.evidence || []) {
        check(app, Object.values(app.fixture.documents).includes(evidence.knowledge_id), 'Evidence is from a fixture document');
        const chunks = await data(app, '/chunks/' + evidence.knowledge_id + '?page=1&page_size=100');
        const source = chunks.find(chunk => chunk.id === evidence.chunk_id);
        const normalize = text => text.normalize('NFKC').replace(/\s+/g, ' ').trim();
        check(app, !!source && source.index_status === 'ready' && source.is_enabled !== false && !!evidence.quote.trim() && normalize(source.content).includes(normalize(evidence.quote)), 'Evidence quote occurs in actual current chunk');
      }
      await item.locator('.answer-result').waitFor();
      check(app, await item.getByRole('radio').first().isDisabled(), 'Answered question cannot be resubmitted in UI');
      await shot(app, 'answer-' + i);
    }
    app.report.quiz.submittedThisRun = submittedThisRun;
    const saved = await data(app, '/learning/question-sets/' + quiz.id);
    if (generatedCardQuiz === quiz.id) generatedCardResults = saved.questions;
    await snapshot(app, 'before-answer-reload'); await app.page.reload({ waitUntil: 'domcontentloaded' });
    await waitUntil(app, async () => await app.page.locator('.learning-quiz > .questions > li > .answer-result').count() === 3, 'persisted answers on reload');
    const reloaded = await data(app, '/learning/question-sets/' + quiz.id);
    check(app, JSON.stringify(saved.questions) === JSON.stringify(reloaded.questions), 'Reload returns identical saved answers and feedback');
    app.report.quiz.serverResults = saved.questions.filter(q => q.answered && q.result).map(q => ({ question_id: q.id, correct: q.result.correct, attempt_id: q.result.attempt_id, mastery: q.result.mastery }));
    check(app, !REPLAY_QUIZ || !app.report.network.some(r => r.method === 'POST' && (r.path.endsWith('/attempts') || r.path.endsWith('/question-sets'))), 'Replay performs no quiz generation or submission');
    await shot(app, 'answers-persisted'); await layout(app, 'answered-desktop');
    await exportUI(app, 'answered-export');
    await app.page.setViewportSize(MOBILE);
    await collapseMobileSidebar(app);
    await app.page.locator('.answer-result').first().scrollIntoViewIfNeeded();
    await shot(app, 'answers-mobile'); await layout(app, 'answered-mobile');
  });
});

async function createRealCard(app, quiz) {
  assert.equal(app.label, 'learner_a', 'Card fixture belongs only to the browser-test actor');
  const agent = await apiCall(app, 'POST', '/agents', {
    name: `Guided learning E2E ${process.pid}`, description: 'Disposable source-backed learning card test; no external tools',
    config: { agent_mode: 'smart-reasoning', agent_type: 'custom', model_id: app.fixture.model_id,
      system_prompt: 'You operate a disposable guided-learning UI test. Read the requested Wiki page using wiki_read_page, then call prepare_learning_quiz exactly once. Never answer a quiz or reveal answer keys. Do not poll. After the tool returns, reply only that the quiz card is ready to open.',
      allowed_tools: ['wiki_read_page', 'prepare_learning_quiz'], kb_selection_mode: 'selected', knowledge_bases: [app.fixture.knowledge_base_id],
      mcp_selection_mode: 'none', skills_selection_mode: 'none', web_search_enabled: false, memory_enabled: false,
      temperature: 0, max_iterations: 4, max_completion_tokens: 1200, thinking: false },
  });
  assert.ok(agent.ok && agent.data.id, 'Restricted fixture Agent creation rejected');
  const created = await apiCall(app, 'POST', '/sessions', { title: 'Guided learning E2E card', description: 'Disposable learner_a source-backed quiz card' });
  assert.ok(created.ok && created.data.id, 'Fixture session creation rejected');
  generatedCardSession = created.data.id;
  await writeJSON('card-fixture.json', { actor: app.label, agent_id: agent.data.id, session_id: generatedCardSession, quiz_id: quiz.id, status: 'agent-request-started' });
  let streamResponse;
  try {
    streamResponse = await app.api.post('/api/v1/agent-chat/' + generatedCardSession, { data: {
      query: `Read Wiki page ${SLUGS[0]} in the selected knowledge base, then prepare its learning quiz once so I can open the quiz card. I will answer it in the UI; do not provide any answers.`,
      knowledge_base_ids: [app.fixture.knowledge_base_id], agent_enabled: true, agent_id: agent.data.id,
      summary_model_id: app.fixture.model_id, web_search_enabled: false, disable_title: true, channel: 'web',
    }, maxRedirects: 0, timeout: 120000 });
  } catch { throw new Error('Agent stream transport failed (sensitive diagnostics suppressed)'); }
  assert.ok(streamResponse.ok(), 'Real Agent stream rejected: HTTP ' + streamResponse.status());
  // HTTP/SSE body is neither printed nor retained. Authoritative persisted
  // tool calls, not an invented stream/card DTO, prove the real integration.
  let calls = [];
  await waitUntil(app, async () => {
    const messages = await data(app, '/messages/' + generatedCardSession + '/load?limit=20');
    calls = messages.flatMap(m => (m.agent_steps || []).flatMap(step => step.tool_calls || []));
    return calls.some(call => (call.name === 'prepare_learning_quiz' || call.target?.name === 'prepare_learning_quiz') && call.result?.success);
  }, 'real persisted quiz-card tool result', 20000);
  const call = calls.find(call => (call.name === 'prepare_learning_quiz' || call.target?.name === 'prepare_learning_quiz') && call.result?.success);
  let output = call.result.data;
  if (!output?.quiz_id) { try { output = JSON.parse(call.result.output); } catch { output = {}; } }
  assert.ok(output.display_type === 'learning_quiz' && output.quiz_id === quiz.id && output.knowledge_base_id === app.fixture.knowledge_base_id, 'Real card must point to the prepared unsubmitted quiz');
  check(app, !/correct_option|selected_option|explanation|"questions"/.test(JSON.stringify(output)), 'Agent tool transcript carries a quiz reference, not answer keys');
  generatedCardQuiz = quiz.id;
  app.report.cardFixture = { session_id: generatedCardSession, agent_id: agent.data.id, quiz_id: quiz.id };
  await writeJSON('card-fixture.json', { actor: app.label, ...app.report.cardFixture, status: 'actual-tool-result-persisted', tools: calls.map(c => ({ name: c.target?.name || c.name, success: c.result?.success })) });
}

async function expandRealCard(app) {
  await app.page.locator('.agent-stream-display').waitFor({ timeout: 30000 });
  await snapshot(app, 'chat-steps');
  const root = app.page.locator('.agent-stream-display .tree-root').first();
  if (await root.count() && !(await app.page.locator('.agent-stream-display .tree-children').count())) {
    await click(app, root, 'expand-real-agent-steps');
  }
  const header = app.page.locator('.agent-stream-display .action-card:not(.thinking-event-card) > .action-header')
    .filter({ hasText: /^Called Prepare practice quiz$/ }).first();
  await header.waitFor();
  if (!await app.page.locator('.learning-panel.compact').count()) await click(app, header, 'expand-real-quiz-tool');
}

test('APPROVAL: real existing learning tool card reloads authoritative quiz and opens Wiki', { skip: !CARD_SESSION && !CREATE_CARD }, async () => {
  const cardSession = CARD_SESSION || generatedCardSession;
  assert.match(cardSession, /^[0-9a-f-]{36}$/i, 'Card session must be a disposable runtime UUID');
  await scenario('approved-real-card', 'learner_a', DESKTOP, async app => {
    await snapshot(app, 'before-real-card');
    await app.page.goto(`${FRONTEND}/platform/chat/${cardSession}`, { waitUntil: 'domcontentloaded' });
    await expandRealCard(app);
    const panel = app.page.locator('.learning-panel.compact').filter({ has: app.page.locator('.learning-quiz') }).first();
    await panel.waitFor({ timeout: 30000 });
    await waitUntil(app, async () => await panel.locator('.learning-quiz > .questions > li').count() === 3, 'real card quiz');
    const quizRequest = app.report.network.find(r => r.status === 200 && r.path.startsWith('/api/v1/learning/question-sets/'));
    assert.ok(quizRequest, 'Card must load a real quiz over HTTP');
    const authoritative = await data(app, quizRequest.path.replace('/api/v1', ''));
    check(app, authoritative.knowledge_base_id === app.fixture.knowledge_base_id && SLUGS.some(s => app.fixture.pages[s].page_id === authoritative.page_id), 'Card quiz belongs to a curated fixture page');
    if (generatedCardResults) check(app, JSON.stringify(authoritative.questions) === JSON.stringify(generatedCardResults), 'Chat card fetches the exact Wiki-submitted answers');
    const text = await panel.innerText();
    await shot(app, 'card');
    await snapshot(app, 'before-card-reload'); await app.page.reload({ waitUntil: 'domcontentloaded' });
    await expandRealCard(app);
    await waitUntil(app, async () => await panel.locator('.learning-quiz > .questions > li').count() === 3, 'real card persisted reload');
    check(app, (await panel.innerText()) === text, 'Real card reload preserves authoritative feedback');
    await shot(app, 'card-reloaded');
    const open = panel.getByRole('button', { name: 'Open page', exact: true });
    await click(app, open, 'card-open-page');
    await app.page.locator('.wiki-reader-title').waitFor();
    check(app, app.page.url().includes('/knowledge-bases/' + app.fixture.knowledge_base_id), 'Card opens the owned fixture Wiki');
    await shot(app, 'card-to-wiki');
  });
});

async function personalExportDigest(label) {
  const publicAPI = await playwright.request.newContext({ baseURL: API });
  let privateAPI;
  try {
    const account = seed.users[label];
    let response;
    try { response = await publicAPI.post('/api/v1/auth/login', { data: { email: account.email, password: account.password }, maxRedirects: 0, timeout: 20000 }); }
    catch { throw new Error('Privacy noninterference login failed; details suppressed'); }
    assert.equal(response.status(), 200, 'Privacy noninterference login rejected');
    const raw = await response.json(); const auth = raw.data || raw; rememberSecrets(auth);
    assert.ok(auth.user?.id === account.user_id && auth.token, 'Privacy noninterference scope mismatch');
    privateAPI = await playwright.request.newContext({ baseURL: API, extraHTTPHeaders: { Authorization: `Bearer ${auth.token}` } });
    const exported = await data({ api: privateAPI }, '/learning/export?knowledge_base_id=' + manifest[label].knowledge_base_id);
    delete exported.exported_at;
    return createHash('sha256').update(JSON.stringify(exported)).digest('hex');
  } finally { await privateAPI?.dispose(); await publicAPI.dispose(); }
}

test('APPROVAL: learner_b export before physical clear; learner_a history preserved', { skip: !CLEAR_B }, async () => {
  await scenario('approved-clear-learner-b', 'learner_b', DESKTOP, async app => {
    await openWiki(app); await ensureEnabled(app);
    const otherBefore = await personalExportDigest('learner_a');
    const before = await exportUI(app, 'before-clear');
    if (!(before.attempts || []).length || !(before.quizzes || []).length) app.report.notes.push('learner_b has no prior answers/quizzes: their deletion is not demonstrated. Run an approved learner_b real quiz first for full destructive coverage.');
    await shot(app, 'retained-before-clear');
    assert.ok(app.report.exportRetained && app.report.screenshots.length, 'Retain evidence before destructive action');
    await click(app, app.page.getByRole('button', { name: 'Learning data', exact: true }), 'privacy');
    await click(app, app.page.getByText('Clear this knowledge base', { exact: true }), 'confirm-clear-dialog');
    await shot(app, 'delete-confirmation');
    app.allowDelete = true;
    const responsePromise = app.page.waitForResponse(r => safeURL(r.url()) === '/api/v1/learning/profile' && r.request().method() === 'DELETE');
    await click(app, app.page.getByRole('button', { name: 'Delete permanently', exact: true }), 'delete-only-learner-b');
    const response = await responsePromise; app.allowDelete = false;
    assert.equal(response.status(), 200, 'Clear failed');
    app.report.clearResult = (await response.json()).data;
    await shot(app, 'cleared');
    const cleared = await data(app, '/learning/export?knowledge_base_id=' + app.fixture.knowledge_base_id);
    check(app, !(cleared.attempts || []).length && !(cleared.quizzes || []).length, 'Deleted answers/quizzes disappear from export');
    check(app, (cleared.nodes || []).every(n => n.mastery.attempts === 0 && !n.familiar), 'All cleared topic records lose prior personal state');
    check(app, await personalExportDigest('learner_a') === otherBefore, 'learner_a export is unchanged by learner_b deletion');
    check(app, app.report.clearResult.deleted_attempts === (before.attempts || []).length, 'Deleted-attempt count matches retained export');
    await snapshot(app, 'before-clear-reload'); await app.page.reload({ waitUntil: 'domcontentloaded' });
    await app.page.locator('.learning-panel').waitFor(); await shot(app, 'clear-reloaded');
    app.report.notes.push('Reload may legitimately record a fresh view; no learner_a DELETE is ever admitted by the safety guard.');
  });
});
