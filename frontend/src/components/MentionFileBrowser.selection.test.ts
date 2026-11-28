import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { runInNewContext } from 'node:vm';
import ts from 'typescript';
import { computed, effectScope, nextTick, reactive, ref, shallowRef, watch, type Ref } from 'vue';
import { UnavailableFilesError, validateFileDraft, type DraftFile, type FileSelection } from '../utils/mentionFileSelection';

const source = readFileSync(new URL('./MentionFileBrowser.vue', import.meta.url), 'utf8');
const draftCode = source.slice(source.indexOf('const draft ='), source.indexOf('interface BrowserRow'));
const watcher = source.slice(
  source.indexOf('watch(() => props.selectedFiles'),
  source.indexOf('watch(() => props.knowledgeBases.map'),
);
const moved: DraftFile = { id: 'moved', name: 'Moved.md', kbId: 'target', folderPath: 'docs' };
const historical = { id: moved.id, name: 'Loading...', kbId: 'old' };
const added = { id: 'added', name: 'Added.md', kbId: 'target', folderPath: '' };

function harness(holdOldRequest = false, wholeKbIds: string[] = []) {
  const props = reactive({
    selectedFiles: [historical] as DraftFile[],
    context: { key: 'chat' },
    knowledgeBases: ['old', 'target'].map(id => ({ id, name: id })),
    supportedFileTypes: ['md'],
    wholeKbIds,
    tagKbIds: [],
  });
  const calls: Array<{ ids: string[]; kbId: string; signal: AbortSignal }> = [];
  const applied: FileSelection[] = [];
  let releaseOldRequest = () => {};
  const scope = effectScope();
  const picker = scope.run(() => runInNewContext(ts.transpile(`${draftCode}\n${watcher}\n({ startSelection, toggleFile, applySelection, draft, selectionError, confirmingConflict })`), {
    props, AbortController, URLSearchParams, shallowRef, ref, computed, watch, nextTick,
    focusSearch() {}, UnavailableFilesError, validateFileDraft,
    emit(event: string, selection: FileSelection) { if (event === 'apply') applied.push(selection); },
    async batchQueryKnowledge(query: string, kbId: string, _agent: string, _tenant: string, signal: AbortSignal) {
      const ids = new URLSearchParams(query).getAll('ids');
      calls.push({ ids, kbId, signal });
      if (kbId === 'old' && holdOldRequest) {
        await new Promise<void>(resolve => { releaseOldRequest = resolve; });
      }
      return { success: true, data: [moved, added]
        .filter(file => file.kbId === kbId && ids.includes(file.id))
        .map(file => ({ id: file.id, title: file.name, file_name: file.name, knowledge_base_id: file.kbId, folder_path: file.folderPath })) };
    },
  })) as {
    startSelection(): void;
    toggleFile(file: DraftFile): void;
    applySelection(): Promise<void>;
    draft: Ref<Map<string, DraftFile> | null>;
    selectionError: Ref<string>;
    confirmingConflict: Ref<boolean>;
  };
  return {
    ...picker,
    calls, applied,
    restore: async () => { props.selectedFiles = [moved]; await nextTick(); },
    releaseOld: () => releaseOldRequest(),
    stop: () => scope.stop(),
  };
}

test('an open draft refreshes a moved file before Apply without losing new selections', async t => {
  const h = harness();
  t.after(h.stop);
  h.startSelection();
  h.toggleFile(added);
  await h.restore();
  assert.equal(h.draft.value?.get(moved.id)?.kbId, 'target');
  assert.equal(h.draft.value?.get(moved.id)?.name, 'Moved.md');
  await h.applySelection();
  assert.deepEqual(h.calls.map(call => call.kbId), ['target']);
  assert.deepEqual(Array.from(h.applied[0]?.files || [], file => [file.id, file.kbId]), [['moved', 'target'], ['added', 'target']]);
  assert.equal(h.selectionError.value, '');
});

test('hydration does not re-add a moved file the user removed from the draft', async t => {
  const h = harness();
  t.after(h.stop);
  h.startSelection();
  h.toggleFile(historical);
  h.toggleFile(added);
  await h.restore();
  await h.applySelection();
  assert.deepEqual(Array.from(h.applied[0]?.files || [], file => file.id), ['added']);
});

test('an ownership update aborts a pending check and revalidates without showing the stale failure', async t => {
  const h = harness(true);
  t.after(h.stop);
  h.startSelection();
  h.toggleFile(added);
  const oldApply = h.applySelection();
  assert.equal(h.calls[0]?.kbId, 'old');
  await h.restore();
  assert.equal(h.calls[0]?.signal.aborted, true);
  h.releaseOld();
  await oldApply;
  await new Promise<void>(resolve => setImmediate(resolve));
  assert.deepEqual(h.calls.map(call => call.kbId), ['old', 'target']);
  assert.equal(h.applied.length, 1);
  assert.equal(h.applied[0]?.files[0]?.kbId, 'target');
  assert.equal(h.selectionError.value, '');
});

test('a pending Apply asks for a new confirmation when restored ownership creates a KB conflict', async t => {
  const h = harness(true, ['target']);
  t.after(h.stop);
  h.startSelection();
  h.toggleFile({ ...added, kbId: 'old' });
  const oldApply = h.applySelection();
  await h.restore();
  h.releaseOld();
  await oldApply;
  assert.equal(h.confirmingConflict.value, true);
  assert.equal(h.applied.length, 0);
});
