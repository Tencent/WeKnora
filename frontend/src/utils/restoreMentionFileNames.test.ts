import assert from 'node:assert/strict';
import test from 'node:test';
import { restoreMentionFileNames } from './restoreMentionFileNames';

function fileRow(id: string, kbId: string) {
  return { id, title: `Title ${id}`, folder_path: 'docs', knowledge_base_id: kbId };
}

const parseIds = (query: string) => new URLSearchParams(query).getAll('ids');

test('file names are restored in bounded requests even when one KB has hundreds of selected files', async () => {
  const ids = Array.from({ length: 220 }, (_, index) => `00000000-0000-4000-8000-${String(index).padStart(12, '0')}`);
  const fileKbIds = Object.fromEntries(ids.map(id => [id, 'large-kb']));
  const requestSizes: number[] = [];
  const files = await restoreMentionFileNames(ids, fileKbIds, undefined, async (query, kbId) => {
    const batchIds = parseIds(query);
    assert.equal(kbId, 'large-kb');
    requestSizes.push(batchIds.length);
    return { success: true, data: batchIds.map(id => fileRow(id, kbId!)) };
  }, new AbortController().signal);

  assert.deepEqual(requestSizes, [40, 40, 40, 40, 40, 20]);
  assert.deepEqual(files.map(file => file.id).sort(), [...ids].sort());
  assert.equal(files[0]?.name, `Title ${ids[0]}`);
  assert.equal(files[0]?.folderPath, 'docs');
});

test('a failed KB lookup does not discard file names restored from other batches', async () => {
  const ids = ['owned-before', 'inaccessible', 'owned-after'];
  const kbByFile = Object.fromEntries(ids.map(id => [id, id]));
  const files = await restoreMentionFileNames(ids, kbByFile, undefined, async (query, kbId) => {
    if (kbId === 'inaccessible') throw { status: 403 };
    if (!kbId) return { success: true, data: [] };
    return { success: true, data: parseIds(query).map(id => fileRow(id, kbId!)) };
  }, new AbortController().signal);

  assert.deepEqual(files.map(file => file.id).sort(), ['owned-after', 'owned-before']);
});

test('files moved since the history was saved recover using their original IDs and current owner', async () => {
  const scope = { agentId: 'history-agent', sourceTenantId: '42' };
  const calls: Array<[string | undefined, string | undefined]> = [];
  const files = await restoreMentionFileNames(['moved'], { moved: 'old-kb' }, scope, async (query, kbId, agentId) => {
    assert.deepEqual(parseIds(query), ['moved']);
    calls.push([kbId, agentId]);
    return { success: true, data: kbId || !agentId ? [] : [fileRow('moved', 'new-kb')] };
  }, new AbortController().signal);
  assert.deepEqual(calls, [['old-kb', undefined], ['old-kb', 'history-agent'], [undefined, undefined], [undefined, 'history-agent']]);
  assert.deepEqual(files.map(({ id, kbId }) => ({ id, kbId })), [{ id: 'moved', kbId: 'new-kb' }]);
});

test('only unresolved files use the restored shared agent and source tenant', async () => {
  const ids = ['direct', 'shared'];
  const kbByFile = { direct: 'same-kb', shared: 'same-kb' };
  const scope = { agentId: 'session-agent', sourceTenantId: '42' };
  const calls: Array<{ ids: string[]; kbId?: string; agentId?: string; sourceTenantId?: string }> = [];
  const files = await restoreMentionFileNames(ids, kbByFile, scope, async (query, kbId, agentId, sourceTenantId) => {
    const batchIds = parseIds(query);
    calls.push({ ids: batchIds, kbId, agentId, sourceTenantId });
    return { success: true, data: [fileRow(agentId ? 'shared' : 'direct', 'same-kb')] };
  }, new AbortController().signal);

  assert.deepEqual(calls, [
    { ids, kbId: 'same-kb', agentId: undefined, sourceTenantId: undefined },
    { ids: ['shared'], kbId: 'same-kb', ...scope },
  ]);
  assert.deepEqual(files.map(file => file.id).sort(), ids);
});
