import assert from 'node:assert/strict';
import test from 'node:test';
import { UnavailableFilesError, validateFileDraft } from './mentionFileSelection';

function row(id: string, kbId: string, parseStatus = 'completed') {
  return { id, knowledge_base_id: kbId, file_name: `${id}.md`, folder_path: '', parse_status: parseStatus };
}

const kbA = { id: 'a', name: 'A' };
const kbB = { id: 'b', name: 'B' };

test('file draft verifies all KB groups with the shared-agent context and restores legacy IDs in draft order', async () => {
  const context = { agent_id: 'agent', agent_source_tenant_id: 'tenant' };
  const calls: string[] = [];
  const files = await validateFileDraft([
    { id: 'first', name: 'cached', kbId: 'a' },
    { id: 'legacy', name: 'unknown' },
    { id: 'second', name: 'cached', kbId: 'a' },
  ], [kbA, kbB], ['md'], async (ids, kb, options) => {
    assert.deepEqual(options, context);
    calls.push(kb);
    return { success: true, data: ids.reverse().map(id => row(id, kb || 'b')) };
  }, context);
  assert.deepEqual(calls, ['a', '']);
  assert.deepEqual(files.map(({ id, kbId, name }) => [id, kbId, name]), [
    ['first', 'a', 'first.md'], ['legacy', 'b', 'legacy.md'], ['second', 'a', 'second.md'],
  ]);
});

test('revoked access, deleted files and unexpected KB ownership prevent any apply', async () => {
  const files = ['ok', 'missing', 'moved', 'deleting', 'forbidden'].map(id => ({
    id, name: id, kbId: id === 'forbidden' ? 'b' : 'a',
  }));
  await assert.rejects(validateFileDraft(files, [kbA, kbB], ['md'], async (_ids, kb) => {
    if (kb === 'b') throw { status: 403 };
    return { success: true, data: [row('ok', 'a'), row('moved', 'b'), row('deleting', 'a', 'deleting')] };
  }, {}), error => {
    assert(error instanceof UnavailableFilesError);
    assert.deepEqual(error.ids, ['missing', 'moved', 'deleting', 'forbidden']);
    return true;
  });
});

test('batch verification remains bounded and cancellation stops pending verification', async () => {
  const files = Array.from({ length: 83 }, (_, i) => ({ id: String(i), name: String(i), kbId: 'a' }));
  const calls: number[] = [];
  await validateFileDraft(files, [kbA], [], async ids => {
    calls.push(ids.length);
    return { success: true, data: ids.map(id => row(id, 'a')) };
  }, {});
  assert.deepEqual(calls, [40, 40, 3]);

  const controller = new AbortController();
  let pendingCalls = 0;
  await assert.rejects(validateFileDraft(files, [kbA], [], async () => {
    pendingCalls++;
    controller.abort();
    return { success: true, data: [] };
  }, { signal: controller.signal }), { name: 'AbortError' });
  assert.equal(pendingCalls, 1);
});
