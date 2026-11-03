import assert from 'node:assert/strict';
import { test } from 'node:test';
import { canDeleteEmptyDocumentFolder, createEmptyFolderDeleteController, createEmptyFolderDeleteState } from './folderDeleteState.ts';

test('only editable empty leaf folders can expose deletion; root and folders with children are excluded', () => {
  const leaf = { path: 'Plans/empty', totalCount: 0, hasChildren: false };
  assert.equal(canDeleteEmptyDocumentFolder(leaf, true), true);
  assert.equal(canDeleteEmptyDocumentFolder(leaf, false), false);
  assert.equal(canDeleteEmptyDocumentFolder({ ...leaf, path: '' }, true), false);
  assert.equal(canDeleteEmptyDocumentFolder({ ...leaf, totalCount: 1 }, true), false);
  assert.equal(canDeleteEmptyDocumentFolder({ ...leaf, hasChildren: true }, true), false);
  assert.equal(canDeleteEmptyDocumentFolder(undefined, true), false);
});

test('delete rechecks live emptiness and permission before issuing a request', async () => {
  const state = createEmptyFolderDeleteState();
  const context = { kbId: 'kb-1', canEdit: true };
  const folder = { path: 'Plans/empty', totalCount: 1, hasChildren: false };
  let calls = 0;
  const c = createEmptyFolderDeleteController(state, {
    currentContext: () => context, folderForPath: () => folder,
    remove: async () => { calls++; }, onDeleted: () => {},
  });
  assert.equal(await c.remove(folder.path), 'ignored');
  folder.totalCount = 0; context.canEdit = false;
  assert.equal(await c.remove(folder.path), 'ignored');
  assert.equal(calls, 0);
});

test('duplicate confirms issue one request and successful deletion names the original parent', async () => {
  const state = createEmptyFolderDeleteState();
  let resolve!: () => void;
  const pending = new Promise<void>(res => { resolve = res; });
  const calls: string[] = [];
  const deleted: unknown[] = [];
  const c = createEmptyFolderDeleteController(state, {
    currentContext: () => ({ kbId: 'kb-1', canEdit: true }),
    folderForPath: path => ({ path, totalCount: 0, hasChildren: false }),
    remove: (kb, path) => { calls.push(`${kb}:${path}`); return pending; },
    onDeleted: folder => { deleted.push(folder); },
  });
  const first = c.remove('Plans/empty');
  assert.equal(await c.remove('Plans/empty'), 'ignored');
  resolve();
  assert.equal(await first, 'deleted');
  assert.deepEqual(calls, ['kb-1:Plans/empty']);
  assert.deepEqual(deleted, [{ kbId: 'kb-1', path: 'Plans/empty', parentPath: 'Plans' }]);
  assert.equal(state.pendingPath, '');
});

test('racing content additions and missing/forbidden targets are errors, never successful deletions', async () => {
  for (const [status, expected] of [[409, 'notEmpty'], [404, 'missing'], [403, 'forbidden'], [500, 'failed']] as const) {
    const state = createEmptyFolderDeleteState();
    let refreshed = false;
    const c = createEmptyFolderDeleteController(state, {
      currentContext: () => ({ kbId: 'kb-1', canEdit: true }),
      folderForPath: path => ({ path, totalCount: 0, hasChildren: false }),
      remove: async () => { throw { status }; }, onDeleted: () => { refreshed = true; },
    });
    assert.equal(await c.remove('empty'), 'failed');
    assert.equal(state.error, expected);
    assert.equal(refreshed, false);
    assert.equal(state.pendingPath, '');
  }
});

test('switching knowledge base while deleting cannot refresh or redirect the new knowledge base', async () => {
  const state = createEmptyFolderDeleteState();
  const context = { kbId: 'old', canEdit: true };
  let resolve!: () => void;
  const pending = new Promise<void>(res => { resolve = res; });
  let refreshed = false;
  const c = createEmptyFolderDeleteController(state, {
    currentContext: () => context,
    folderForPath: path => ({ path, totalCount: 0, hasChildren: false }),
    remove: async () => pending, onDeleted: () => { refreshed = true; },
  });
  const first = c.remove('Plans/empty');
  context.kbId = 'new'; resolve();
  assert.equal(await first, 'ignored');
  assert.equal(refreshed, false);
  assert.equal(state.error, null);
  assert.equal(state.pendingPath, '');
});

test('refresh failure after deletion is distinct from an unsuccessful delete', async () => {
  const state = createEmptyFolderDeleteState();
  const c = createEmptyFolderDeleteController(state, {
    currentContext: () => ({ kbId: 'kb-1', canEdit: true }),
    folderForPath: path => ({ path, totalCount: 0, hasChildren: false }),
    remove: async () => {}, onDeleted: async () => { throw new Error('refresh failed'); },
  });
  assert.equal(await c.remove('empty'), 'refreshFailed');
  assert.equal(state.error, null);
  assert.equal(state.pendingPath, '');
});
