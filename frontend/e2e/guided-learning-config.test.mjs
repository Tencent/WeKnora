import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  FIXTURE_SLUGS, normalizeLoopbackOrigin, parseFixtureJSON,
  resolveRuntimePaths, validateFixtureConfig,
} from './guided-learning-config.mjs';
import {
  createEvidenceDirectory, loadFixtureRuntime, readFixtureManifest, validateOwnedEntry,
} from './guided-learning-runtime.mjs';

const id = n => `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`;
const canary = 'offline-credential-canary';

function fixtures() {
  const state = { api_base: 'http://127.0.0.1:28081', run_id: 'abcdef123456', users: {} };
  for (const [index, label] of ['learner_a', 'learner_b'].entries()) {
    const offset = 100 * index;
    state.users[label] = {
      username: `gl_${label}_${state.run_id}`, email: `gl-${label}-${state.run_id}@example.invalid`,
      password: canary, user_id: id(offset + 1), tenant_id: index + 1,
      knowledge_base_id: id(offset + 2), model_id: id(offset + 3),
      documents: Object.fromEntries(FIXTURE_SLUGS.map((slug, i) => [slug, id(offset + 10 + i)])),
      pages: Object.fromEntries(FIXTURE_SLUGS.map((slug, i) => [slug, {
        page_id: id(offset + 20 + i), chunk_ids: [id(offset + 30 + i)],
      }])),
    };
  }
  const snapshot = { fixtures: Object.fromEntries(Object.entries(state.users).map(([label, user]) => [label,
    Object.fromEntries(['tenant_id', 'knowledge_base_id', 'model_id', 'documents', 'pages']
      .map(key => [key, structuredClone(user[key])])),
  ])) };
  return { state, snapshot };
}

test('legacy state and public fixture snapshot work without version or frontend markers', () => {
  const { state, snapshot } = fixtures();
  const before = structuredClone({ state, snapshot });
  const config = validateFixtureConfig(state, snapshot);
  assert.equal(config.apiBase, 'http://127.0.0.1:28081');
  assert.equal(config.frontendBase, 'http://127.0.0.1:25173');
  assert.deepEqual({ state, snapshot }, before);
});

for (const host of ['127.0.0.1', 'localhost', '[::1]']) {
  test(`HTTP loopback ${host} supports arbitrary ports and API suffix normalization`, () => {
    for (const port of ['', ':1', ':80', ':18082', ':65535']) {
      const origin = `http://${host}${port === ':80' ? '' : port}`;
      const raw = `http://${host}${port}`;
      for (const suffix of ['', '/']) assert.equal(normalizeLoopbackOrigin(raw + suffix), origin);
      for (const suffix of ['', '/', '/api/v1', '/api/v1/']) {
        assert.equal(normalizeLoopbackOrigin(raw + suffix, { api: true }), origin);
      }
    }
    const { state, snapshot } = fixtures();
    state.api_base = `http://${host}:18082/api/v1/`;
    state.frontend_base = `http://${host}:15174/`;
    state.fixture_version = snapshot.fixture_version = 1;
    snapshot.api_base = `http://${host}:18082`;
    snapshot.frontend_base = `http://${host}:15174`;
    const config = validateFixtureConfig(state, snapshot, { GL_API_BASE: snapshot.api_base });
    assert.equal(config.apiBase, snapshot.api_base);
    assert.equal(config.frontendBase, snapshot.frontend_base);
  });
}

for (const value of [
  undefined, null, 42, '', 'https://localhost:28081', 'file:///tmp/seed', '//localhost:28081',
  'http://example.com', 'http://10.37.40.48:25173', 'http://127.0.0.2', 'http://0.0.0.0',
  'http://localhost.example.com', 'http://localhost.', 'http://127.1', 'http://2130706433',
  'http://0x7f000001', 'http://0177.0.0.1', 'http://%6cocalhost', 'http://[::ffff:127.0.0.1]',
  'http://user:password@localhost', 'http://@localhost', 'http://localhost?', 'http://localhost#',
  'http://localhost/?token=secret', 'http://localhost/#secret', 'http://localhost/path',
  'http://localhost//', 'http://localhost/.', 'http://localhost/a/..', 'http://localhost/%2e',
  'http://localhost/api/v1/..', 'http://localhost/api/v1//', 'http://localhost/api%2fv1',
  'http://localhost:0', 'http://localhost:65536', 'http://localhost:', 'http://localhost:+80',
  ' http://localhost', 'http://local\thost', 'http://localhost\n', 'http://localhost\\',
]) {
  test(`reject unsafe origin ${JSON.stringify(value)}`, () => {
    assert.throws(() => normalizeLoopbackOrigin(value));
    assert.throws(() => normalizeLoopbackOrigin(value, { api: true }));
  });
}

test('frontend never accepts an API path, while canonical case and default port normalize', () => {
  assert.throws(() => normalizeLoopbackOrigin('http://localhost/api/v1'));
  assert.equal(normalizeLoopbackOrigin('HTTP://LOCALHOST:80/'), 'http://localhost');
});

test('API override must match the saved canonical origin, not a loopback alias or new port', () => {
  const { state, snapshot } = fixtures();
  for (const base of ['http://127.0.0.1:28081', 'http://127.0.0.1:28081/', 'http://127.0.0.1:28081/api/v1/']) {
    assert.equal(validateFixtureConfig(state, snapshot, { GL_API_BASE: base }).apiBase, state.api_base);
  }
  for (const base of ['http://127.0.0.1:28082', 'http://localhost:28081', 'http://[::1]:28081', '']) {
    assert.throws(() => validateFixtureConfig(state, snapshot, { GL_API_BASE: base }));
  }
});

test('explicit frontend override is loopback-only and never overrides saved API', () => {
  const { state, snapshot } = fixtures();
  state.frontend_base = snapshot.frontend_base = 'http://localhost:15173';
  const config = validateFixtureConfig(state, snapshot, { GL_FRONTEND_BASE: 'http://[::1]:55173/' });
  assert.equal(config.frontendBase, 'http://[::1]:55173');
  assert.equal(config.apiBase, state.api_base);
  for (const base of ['http://10.37.40.48:25173', 'http://localhost/path', '']) {
    assert.throws(() => validateFixtureConfig(state, snapshot, { GL_FRONTEND_BASE: base }));
  }
});

test('manifest metadata, versions and fixture identities must agree before login', () => {
  const changes = [
    ({ state }) => { delete state.api_base; },
    ({ state }) => { state.frontend_base = 'http://example.com'; },
    ({ snapshot }) => { snapshot.api_base = 'http://localhost:28081'; },
    ({ snapshot }) => { snapshot.frontend_base = 'http://localhost:25173'; },
    ({ snapshot }) => { delete snapshot.fixtures.learner_b; },
    ({ state }) => { state.users.production = state.users.learner_a; },
    ({ state }) => { state.users.learner_a.email = 'someone@example.com'; },
    ({ state }) => { state.users.learner_a.username = 'someone'; },
    ({ state }) => { state.users.learner_a.password = ''; },
    ({ state }) => { state.users.learner_a.user_id = '../foreign-user'; },
    ({ state }) => { state.users.learner_a.tenant_id = Number.MAX_SAFE_INTEGER + 1; },
    ({ state }) => { state.users.learner_a = null; },
    ({ snapshot }) => { snapshot.fixtures.learner_a.user_id = id(999); },
    ({ snapshot }) => { snapshot.fixtures.learner_a.model_id = id(999); },
    ({ snapshot }) => { snapshot.fixtures.learner_a.documents[FIXTURE_SLUGS[0]] = id(999); },
    ({ snapshot }) => { snapshot.fixtures.learner_a.pages[FIXTURE_SLUGS[0]].page_id = id(999); },
    ({ snapshot }) => { snapshot.fixtures.learner_a.pages[FIXTURE_SLUGS[0]].chunk_ids = [id(999)]; },
  ];
  for (const key of ['tenant_id', 'knowledge_base_id', 'user_id']) {
    changes.push(({ state, snapshot }) => {
      state.users.learner_b[key] = state.users.learner_a[key];
      if (key !== 'user_id') snapshot.fixtures.learner_b[key] = state.users.learner_a[key];
    });
  }
  for (const document of ['state', 'snapshot']) for (const version of [null, '1', 0, 2]) {
    changes.push(value => { value[document].fixture_version = version; });
  }
  for (const change of changes) {
    const input = fixtures();
    change(input);
    assert.throws(() => validateFixtureConfig(input.state, input.snapshot), error => {
      assert.ok(!error.message.includes(canary));
      assert.equal(error.cause, undefined);
      return true;
    });
  }
  assert.throws(() => validateFixtureConfig(null, {}));
  assert.throws(() => validateFixtureConfig({}, []));
});

test('malformed fixture JSON and bad origin diagnostics suppress credential values', () => {
  assert.throws(() => parseFixtureJSON(`{"password":"${canary}",broken}`), error => {
    assert.ok(!error.message.includes(canary));
    assert.equal(error.cause, undefined);
    return true;
  });
  assert.throws(() => normalizeLoopbackOrigin(`http://${canary}@localhost`), error => !error.message.includes(canary));
});

test('runtime and evidence paths stay under the selected checkout runtime', () => {
  const repo = '/checkout';
  const defaults = resolveRuntimePaths(repo, {}, 'run');
  assert.equal(defaults.stateFile, '/checkout/.runtime/seed-accounts.json');
  assert.equal(defaults.evidenceDir, '/checkout/.runtime/evidence/run');
  for (const runtime of ['.runtime/batch/case', '/checkout/.runtime/batch/case']) {
    const paths = resolveRuntimePaths(repo, { GL_RUNTIME_DIR: runtime }, 'run');
    assert.equal(paths.evidenceDir, '/checkout/.runtime/batch/case/evidence/run');
    assert.equal(paths.manifestFile, '/checkout/.runtime/batch/case/evidence/seed.json');
  }
  for (const value of ['', '..', '.runtime/../foreign', '.runtime-old', '/foreign/.runtime', '/checkout', 'file:///tmp']) {
    assert.throws(() => resolveRuntimePaths(repo, { GL_RUNTIME_DIR: value }));
  }
  for (const value of ['', '.runtime/evidence/run', '.runtime/case/evidence', '.runtime/case/evidence-old/run',
    '.runtime/case/evidence/../run', '/foreign/run']) {
    assert.throws(() => resolveRuntimePaths(repo, { GL_RUNTIME_DIR: '.runtime/case', GL_EVIDENCE_DIR: value }));
  }
  assert.equal(resolveRuntimePaths(repo, {
    GL_RUNTIME_DIR: '.runtime/case', GL_EVIDENCE_DIR: '.runtime/case/evidence/nested/run',
  }).evidenceDir, '/checkout/.runtime/case/evidence/nested/run');
});

test('owned-entry policy rejects foreign ownership, links, writable or nonregular files', () => {
  const info = { uid: 1000, mode: 0o600, nlink: 1, size: 100,
    isFile: () => true, isDirectory: () => false, isSymbolicLink: () => false };
  validateOwnedEntry(info, 1000, { privateFile: true });
  for (const change of [{ uid: 1001 }, { mode: 0o644 }, { mode: 0o660 }, { mode: 0o4600 },
    { nlink: 2 }, { size: 1024 * 1024 + 1 }, { isFile: () => false }, { isSymbolicLink: () => true }]) {
    assert.throws(() => validateOwnedEntry({ ...info, ...change }, 1000, { privateFile: true }));
  }
  assert.throws(() => validateOwnedEntry({ ...info, mode: 0o777, isDirectory: () => true }, 1000, { directory: true }));
});

async function temporaryRuntime(t, nested = false) {
  // Scratch fixtures live beside this test, never in the checkout's .runtime.
  const repo = await fs.mkdtemp(path.join(path.dirname(fileURLToPath(import.meta.url)), '.offline-fixtures-'));
  t.after(() => fs.rm(repo, { recursive: true, force: true }));
  const env = nested ? { GL_RUNTIME_DIR: '.runtime/batch/case' } : {};
  const paths = resolveRuntimePaths(repo, env, 'run');
  await fs.mkdir(paths.evidenceRoot, { mode: 0o700, recursive: true });
  const { state, snapshot } = fixtures();
  await fs.writeFile(paths.stateFile, JSON.stringify(state), { mode: 0o600 });
  await fs.writeFile(paths.manifestFile, JSON.stringify(snapshot), { mode: 0o600 });
  return { repo, env, paths };
}

test('loader uses only scratch state, is read-only, and creates a new private evidence run', async t => {
  const { repo, env, paths } = await temporaryRuntime(t, true);
  const before = await fs.readFile(paths.stateFile, 'utf8');
  const runtime = await loadFixtureRuntime(repo, env, 'run');
  assert.deepEqual(runtime.paths, paths);
  assert.equal(runtime.manifestText, await readFixtureManifest(paths));
  assert.equal(await fs.readFile(paths.stateFile, 'utf8'), before);
  await assert.rejects(fs.stat(paths.evidenceDir), { code: 'ENOENT' });
  await createEvidenceDirectory(paths);
  assert.equal((await fs.stat(paths.evidenceDir)).mode & 0o777, 0o700);
  await assert.rejects(createEvidenceDirectory(paths));
  await assert.rejects(loadFixtureRuntime(repo, env, 'run'));
});

for (const target of ['runtimeRoot', 'runtimeDir', 'evidenceRoot', 'stateFile', 'manifestFile']) {
  test(`loader rejects ${target} symlinks even when the target is inside the checkout`, async t => {
    const { repo, env, paths } = await temporaryRuntime(t, true);
    await fs.rename(paths[target], paths[target] + '-real');
    await fs.symlink(paths[target] + '-real', paths[target]);
    await assert.rejects(loadFixtureRuntime(repo, env, 'run'));
  });
}

test('loader rejects symlink ancestors and evidence parents, including inward links', async t => {
  const { repo, env, paths } = await temporaryRuntime(t, true);
  const parent = path.dirname(paths.runtimeDir);
  await fs.rename(parent, parent + '-real');
  await fs.symlink(parent + '-real', parent);
  await assert.rejects(loadFixtureRuntime(repo, env, 'run'));
  await fs.unlink(parent);
  await fs.rename(parent + '-real', parent);
  await fs.symlink(paths.evidenceRoot, path.join(paths.evidenceRoot, 'alias'));
  await assert.rejects(loadFixtureRuntime(repo, { ...env, GL_EVIDENCE_DIR: path.join(paths.evidenceRoot, 'alias/run') }, 'run'));
});

for (const target of ['stateFile', 'manifestFile']) {
  test(`loader rejects ${target} hard links and unsafe modes`, async t => {
    const { repo, env, paths } = await temporaryRuntime(t);
    await fs.link(paths[target], path.join(repo, 'hard-link'));
    await assert.rejects(loadFixtureRuntime(repo, env, 'run'));
    await fs.unlink(path.join(repo, 'hard-link'));
    await fs.chmod(paths[target], target === 'stateFile' ? 0o644 : 0o666);
    await assert.rejects(loadFixtureRuntime(repo, env, 'run'));
  });
}

test('loader refuses missing state, mismatched API and malformed secrets without creating evidence', async t => {
  const { repo, env, paths } = await temporaryRuntime(t);
  await assert.rejects(loadFixtureRuntime(repo, { ...env, GL_API_BASE: 'http://localhost:28081' }, 'run'));
  await fs.writeFile(paths.stateFile, `{"password":"${canary}",broken}`);
  await assert.rejects(loadFixtureRuntime(repo, env, 'run'), error => {
    assert.ok(!error.message.includes(canary));
    assert.equal(error.cause, undefined);
    return true;
  });
  await fs.unlink(paths.stateFile);
  await assert.rejects(loadFixtureRuntime(repo, env, 'run'));
  await assert.rejects(fs.stat(paths.evidenceDir), { code: 'ENOENT' });
});

test('absolute paths accept an OS parent alias but never a foreign checkout', async t => {
  const { repo, paths } = await temporaryRuntime(t);
  assert.equal((await loadFixtureRuntime(repo, { GL_RUNTIME_DIR: paths.runtimeDir }, 'run')).paths.runtimeDir, paths.runtimeDir);
  const alias = path.join(repo, 'parent-alias');
  await fs.symlink(path.dirname(repo), alias);
  const aliasedRepo = path.join(alias, path.basename(repo));
  assert.equal((await loadFixtureRuntime(repo, { GL_RUNTIME_DIR: path.join(aliasedRepo, '.runtime') }, 'run')).paths.runtimeDir, paths.runtimeDir);
  const foreign = await temporaryRuntime(t);
  await assert.rejects(loadFixtureRuntime(repo, { GL_RUNTIME_DIR: foreign.paths.runtimeDir }, 'run'));
});
