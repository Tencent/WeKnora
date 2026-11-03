import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createDocumentFolderController, createDocumentFolderState, validateDocumentFolderName, type FolderCreateContext } from './folderCreateState.ts';

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}

function fixture(context: FolderCreateContext = { kbId: 'kb-1', parentPath: 'Plans', canEdit: true }) {
  const state = createDocumentFolderState();
  const calls: Array<{ kbId: string; parent_path: string; name: string }> = [];
  const created: unknown[] = [];
  const response = deferred<{ data: { path: string } }>();
  const controller = createDocumentFolderController(state, {
    currentContext: () => context,
    existingPaths: () => ['Plans/existing', 'Other/same-name'],
    create: (kbId, payload) => { calls.push({ kbId, ...payload }); return response.promise; },
    onCreated: folder => { created.push(folder); },
  });
  return { state, context, calls, created, response, ...controller };
}

test('strict single-segment validation rejects unsafe names before normalization', () => {
  for (const name of ['', '   ', '.', '..', 'a/b', 'a／b', 'a∕b', 'a⁄b', 'a\\b', 'a\n', '\tname', 'a\0b', 'name.', 'name. ']) {
    assert.equal(validateDocumentFolderName(name, '', []), 'invalid', JSON.stringify(name));
  }
  assert.equal(validateDocumentFolderName(' 财务资料 ', 'Plans', []), null);
});

test('UTF-8 byte length, full path length and directory depth match server limits', () => {
  assert.equal(validateDocumentFolderName('文'.repeat(42), '', []), null);
  assert.equal(validateDocumentFolderName('文'.repeat(43), '', []), 'tooLong');
  assert.equal(validateDocumentFolderName('😀'.repeat(32), '', []), null);
  assert.equal(validateDocumentFolderName('😀'.repeat(33), '', []), 'tooLong');
  assert.equal(validateDocumentFolderName('new', 'x'.repeat(1021), []), 'tooLong');
  assert.equal(validateDocumentFolderName('new', Array(15).fill('x').join('/'), []), null);
  assert.equal(validateDocumentFolderName('new', Array(16).fill('x').join('/'), []), 'tooDeep');
});

test('duplicate names are scoped to the selected parent', () => {
  assert.equal(validateDocumentFolderName('existing', 'Plans', ['Plans/existing']), 'duplicate');
  assert.equal(validateDocumentFolderName('existing', 'Other', ['Plans/existing']), null);
});

test('cancel and validation failure never issue a creation request', async () => {
  const f = fixture();
  f.open(); f.state.name = 'new'; f.cancel();
  assert.equal(await f.submit(), 'ignored');
  f.open(); f.state.name = 'existing';
  assert.equal(await f.submit(), 'invalid');
  assert.equal(f.state.error, 'duplicate');
  assert.equal(f.calls.length, 0);
});

test('double submit coalesces; cancel is blocked while request is pending', async () => {
  const f = fixture();
  f.open(); f.state.name = ' New Folder ';
  const pending = f.submit();
  assert.equal(await f.submit(), 'ignored');
  assert.equal(f.cancel(), false);
  assert.deepEqual(f.calls, [{ kbId: 'kb-1', parent_path: 'Plans', name: 'New Folder' }]);
  f.response.resolve({ data: { path: 'Plans/New Folder' } });
  assert.equal(await pending, 'created');
  assert.deepEqual(f.created, [{ kbId: 'kb-1', parentPath: 'Plans', path: 'Plans/New Folder' }]);
  assert.equal(f.state.visible, false);
  assert.equal(f.state.pending, false);
});

test('root creation sends an empty parent path', async () => {
  const f = fixture({ kbId: 'kb-1', parentPath: '', canEdit: true });
  f.open(); f.state.name = 'root-child';
  const pending = f.submit();
  assert.equal(f.calls[0].parent_path, '');
  f.response.resolve({ data: { path: 'root-child' } });
  assert.equal(await pending, 'created');
});

test('server failures retain the name and permit retry, including racing duplicates', async () => {
  for (const [status, expected] of [[400, 'invalid'], [403, 'forbidden'], [404, 'parentMissing'], [409, 'duplicate'], [500, 'failed']] as const) {
    const state = createDocumentFolderState();
    let calls = 0;
    const c = createDocumentFolderController(state, {
      currentContext: () => ({ kbId: 'kb', parentPath: '', canEdit: true }),
      existingPaths: () => [],
      create: async () => { if (++calls === 1) throw { status }; return { data: { path: 'new' } }; },
      onCreated: () => {},
    });
    c.open(); state.name = 'new';
    assert.equal(await c.submit(), 'failed');
    assert.equal(state.name, 'new');
    assert.equal(state.error, expected);
    assert.equal(state.visible, true);
    assert.equal(await c.submit(), 'created');
    assert.equal(calls, 2);
  }
});

test('navigation and permission changes invalidate late success or errors', async () => {
  for (const outcome of ['success', 'error'] as const) {
    const f = fixture();
    f.open(); f.state.name = 'new';
    const pending = f.submit();
    f.context.parentPath = 'Other'; f.contextChanged();
    assert.equal(f.state.visible, false);
    assert.equal(f.open(), false, 'old pending request prevents another submission');
    if (outcome === 'success') f.response.resolve({ data: { path: 'Plans/new' } });
    else f.response.reject({ status: 500 });
    assert.equal(await pending, 'ignored');
    assert.deepEqual(f.created, []);
    assert.equal(f.state.error, null);
    assert.equal(f.open(), true);
    assert.equal(f.state.parentPath, 'Other');
  }
  const f = fixture(); f.open(); f.state.name = 'new';
  f.context.canEdit = false;
  assert.equal(await f.submit(), 'ignored');
  assert.equal(f.open(), false);
  assert.equal(f.calls.length, 0);
});

test('an API success followed by refresh failure stays closed and is not retried as creation', async () => {
  const state = createDocumentFolderState();
  let calls = 0;
  const c = createDocumentFolderController(state, {
    currentContext: () => ({ kbId: 'kb', parentPath: '', canEdit: true }),
    existingPaths: () => [],
    create: async () => { calls++; return { data: { path: 'new' } }; },
    onCreated: async () => { throw new Error('refresh failed'); },
  });
  c.open(); state.name = 'new';
  assert.equal(await c.submit(), 'refreshFailed');
  assert.equal(state.visible, false);
  assert.equal(state.pending, false);
  assert.equal(await c.submit(), 'ignored');
  assert.equal(calls, 1);
});
