import path from 'node:path';
import { isDeepStrictEqual } from 'node:util';

export const FIXTURE_SLUGS = Object.freeze([
  'concept/topic4-retrieval', 'concept/topic4-feedback', 'concept/topic4-review',
]);
const LABELS = ['learner_a', 'learner_b'];
const LEGACY_FRONTEND = 'http://127.0.0.1:25173';
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

// Errors must never carry input values: fixture state contains credentials.
export function requireSafe(condition, message) {
  if (!condition) throw new Error(message);
}

export function normalizeLoopbackOrigin(value, { api = false } = {}) {
  const message = 'Expected an HTTP loopback origin without credentials, query, fragment or extra path';
  requireSafe(typeof value === 'string' && !/[\s\\]/.test(value), message);
  // Check literal syntax before URL normalization erases dot paths or empty
  // query/fragment delimiters, or accepts noncanonical numeric host spellings.
  const literal = /^http:\/\/(?:127\.0\.0\.1|localhost|\[::1\])(?::[0-9]+)?(\/[^?#]*)?$/i.exec(value);
  const allowedPaths = api ? ['', '/', '/api/v1', '/api/v1/'] : ['', '/'];
  requireSafe(literal && allowedPaths.includes(literal[1] || ''), message);
  let url;
  try { url = new URL(value); } catch { throw new Error(message); }
  requireSafe(!url.port || Number(url.port) > 0, message);
  return url.origin;
}

export function validatePathInput(value) {
  requireSafe(typeof value === 'string' && value.length > 0
    && !/[\x00-\x1f\x7f\\]/.test(value) && !value.split(path.sep).includes('..'),
  'Runtime paths must be nonempty local paths without parent traversal');
}

export function isWithin(parent, target, allowRoot = false) {
  const relative = path.relative(parent, target);
  return (allowRoot && relative === '')
    || (relative !== '' && relative !== '..' && !relative.startsWith('..' + path.sep) && !path.isAbsolute(relative));
}

// Relative environment paths are checkout-relative, independent of npm's cwd.
export function resolveRuntimePaths(repo, env = {}, runName = 'guided-learning') {
  const root = path.resolve(repo);
  const runtimeRoot = path.join(root, '.runtime');
  const requestedRuntime = env.GL_RUNTIME_DIR ?? runtimeRoot;
  validatePathInput(requestedRuntime);
  const runtimeDir = path.resolve(root, requestedRuntime);
  requireSafe(isWithin(runtimeRoot, runtimeDir, true), 'GL_RUNTIME_DIR must stay under this checkout .runtime');
  const evidenceRoot = path.join(runtimeDir, 'evidence');
  const requestedEvidence = env.GL_EVIDENCE_DIR ?? path.join(evidenceRoot, runName);
  validatePathInput(requestedEvidence);
  const evidenceDir = path.resolve(root, requestedEvidence);
  requireSafe(isWithin(evidenceRoot, evidenceDir), 'Evidence must stay below the chosen runtime evidence directory');
  return {
    root, runtimeRoot, runtimeDir, evidenceRoot, evidenceDir,
    stateFile: path.join(runtimeDir, 'seed-accounts.json'),
    manifestFile: path.join(evidenceRoot, 'seed.json'),
  };
}

export function parseFixtureJSON(text) {
  try { return JSON.parse(text); } catch { throw new Error('Invalid fixture JSON (contents suppressed)'); }
}

function record(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function exactKeys(value, keys) {
  return record(value) && isDeepStrictEqual(Object.keys(value).sort(), [...keys].sort());
}

function identifier(value) {
  return typeof value === 'string' && UUID.test(value);
}

function tenantID(value) {
  return (Number.isSafeInteger(value) && value > 0)
    || (typeof value === 'string' && /^[1-9][0-9]*$/.test(value));
}

export function validateFixtureConfig(state, snapshot, env = {}) {
  requireSafe(record(state) && record(snapshot), 'Fixture state and manifest must be objects');
  for (const document of [state, snapshot]) {
    requireSafe(!Object.hasOwn(document, 'fixture_version') || document.fixture_version === 1,
      'Unsupported fixture version');
  }
  const apiBase = normalizeLoopbackOrigin(state.api_base, { api: true });
  const savedFrontend = Object.hasOwn(state, 'frontend_base')
    ? normalizeLoopbackOrigin(state.frontend_base) : LEGACY_FRONTEND;
  if (env.GL_API_BASE !== undefined) {
    requireSafe(normalizeLoopbackOrigin(env.GL_API_BASE, { api: true }) === apiBase,
      'GL_API_BASE must equal the saved canonical API origin');
  }
  if (Object.hasOwn(snapshot, 'api_base')) {
    requireSafe(normalizeLoopbackOrigin(snapshot.api_base, { api: true }) === apiBase,
      'Fixture manifest API does not match saved state');
  }
  if (Object.hasOwn(snapshot, 'frontend_base')) {
    requireSafe(normalizeLoopbackOrigin(snapshot.frontend_base) === savedFrontend,
      'Fixture manifest frontend does not match saved state');
  }
  const frontendBase = env.GL_FRONTEND_BASE === undefined
    ? savedFrontend : normalizeLoopbackOrigin(env.GL_FRONTEND_BASE);
  requireSafe(exactKeys(state.users, LABELS) && exactKeys(snapshot.fixtures, LABELS),
    'Exactly two disposable learner fixtures are required');
  const seenUsers = new Set();
  const seenTenants = new Set();
  const seenKBs = new Set();
  const seenDocuments = new Set();
  const seenPages = new Set();
  for (const label of LABELS) {
    const user = state.users[label];
    const fixture = snapshot.fixtures[label];
    requireSafe(record(user) && record(fixture), 'Missing disposable fixture');
    requireSafe(typeof user.username === 'string' && user.username.startsWith(`gl_${label}_`)
      && user.username.length > `gl_${label}_`.length
      && typeof user.email === 'string' && /^[^\s@]+@example\.invalid$/.test(user.email)
      && typeof user.password === 'string' && user.password.length > 0,
    'Refuse non-disposable or incomplete account credentials');
    requireSafe(identifier(user.user_id) && identifier(user.knowledge_base_id)
      && identifier(user.model_id) && tenantID(user.tenant_id), 'Invalid disposable fixture identity');
    for (const key of ['tenant_id', 'knowledge_base_id', 'model_id', 'documents', 'pages']) {
      requireSafe(isDeepStrictEqual(user[key], fixture[key]), 'Fixture manifest does not match saved identity or sources');
    }
    if (Object.hasOwn(fixture, 'user_id')) {
      requireSafe(fixture.user_id === user.user_id, 'Fixture manifest user does not match saved state');
    }
    requireSafe(!seenUsers.has(user.user_id) && !seenTenants.has(String(user.tenant_id))
      && !seenKBs.has(user.knowledge_base_id), 'Disposable fixtures must have distinct users, tenants and knowledge bases');
    seenUsers.add(user.user_id);
    seenTenants.add(String(user.tenant_id));
    seenKBs.add(user.knowledge_base_id);
    requireSafe(exactKeys(user.documents, FIXTURE_SLUGS) && exactKeys(user.pages, FIXTURE_SLUGS),
      'Expected only the three curated fixture documents and pages');
    for (const slug of FIXTURE_SLUGS) {
      const page = user.pages[slug];
      requireSafe(identifier(user.documents[slug]) && record(page) && identifier(page.page_id)
        && Array.isArray(page.chunk_ids) && page.chunk_ids.every(identifier),
      'Invalid curated source or page identity');
      requireSafe(!seenDocuments.has(user.documents[slug]) && !seenPages.has(page.page_id),
        'Curated fixture documents and pages must be distinct');
      seenDocuments.add(user.documents[slug]);
      seenPages.add(page.page_id);
    }
  }
  return { apiBase, frontendBase, state, fixtures: snapshot.fixtures };
}
