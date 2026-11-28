import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { runInNewContext } from 'node:vm';
import ts from 'typescript';
import { computed, effectScope, nextTick, reactive, ref, watch } from 'vue';
import { restoreMentionFileNames } from '../utils/restoreMentionFileNames';

// Execute the existing confirmation handler, including its real caret cleanup.
// The browser emits the same file item as recent/global search; there is no new submission protocol.
const source = readFileSync(new URL('./Input-field.vue', import.meta.url), 'utf8');
const selection = source.slice(source.indexOf('const onMentionSelect ='), source.indexOf('const removeFile ='));
const settings = readFileSync(new URL('../stores/settings.ts', import.meta.url), 'utf8');
const params = settings.slice(settings.indexOf('getSuggestedQuestionsParams(limit?: number)'), settings.indexOf('// 选择智能体（'));

function harness(button = false) {
  const query = ref(button ? 'before after' : 'before @alpha after');
  const showMention = ref(true);
  const fileList = ref<Array<{ id: string; name: string }>>([]);
  const ids: string[] = [], kbs: string[] = [], map: Record<string, string> = {};
  const fileIdToKbId = ref(map);
  let commits = 0;
  const textarea = { selectionStart: button ? 7 : 13, selectionEnd: button ? 7 : 13, focused: false, focus() { this.focused = true; } };
  const ticks: Array<() => void> = [];
  const store = {
    settings: { selectedTags: [] },
    addFile(id: string) { if (!ids.includes(id)) ids.push(id); },
    addKnowledgeBase(id: string) { kbs.push(id); },
    setFileKbMap(updates: Record<string, string>) { Object.assign(map, updates); },
    applyFileSelection(files: Array<{ id: string; kbId: string }>, replace: string[]) {
      commits++;
      ids.splice(0, ids.length, ...files.map(file => file.id));
      Object.assign(map, Object.fromEntries(files.map(file => [file.id, file.kbId])));
      for (const id of replace) {
        const index = kbs.indexOf(id);
        if (index >= 0) kbs.splice(index, 1);
      }
    },
    getSelectedFiles: () => ids, getSelectedKnowledgeBases: () => kbs,
  };
  const globals = { query, showMention, fileList, fileIdToKbId, settingsStore: store,
    mentionBrowserContext: ref({ key: 'current' }),
    getTextareaEl: () => textarea, isMentionTriggeredByButton: ref(button), mentionStartPos: ref(7), nextTick: (fn: () => void) => ticks.push(fn) };
  const { select, apply } = runInNewContext(ts.transpile(`${selection}\n({ select: onMentionSelect, apply: applyMentionFileSelection })`), globals);
  const serialize = runInNewContext(ts.transpile(`({${params}})`)).getSuggestedQuestionsParams;
  return { select, apply, query, showMention, fileList, fileIdToKbId, ids, kbs, map, textarea, commits: () => commits,
    flush: () => ticks.splice(0).forEach(fn => fn()), request: () => serialize.call(store) };
}

test('confirmed file draft commits together, caches names and restores the mention caret', () => {
  const h = harness();
  h.kbs.push('same', 'other');
  h.apply({ contextKey: 'current', replaceWholeKbIds: ['same'], files: [
    { id: 'doc-a', name: 'one.md', kbId: 'same' },
    { id: 'doc-b', name: 'two.md', kbId: 'second' },
  ] });
  h.flush();
  assert.equal(h.commits(), 1);
  assert.deepEqual(h.ids, ['doc-a', 'doc-b']);
  assert.deepEqual(h.kbs, ['other']);
  assert.equal(h.fileList.value.map(file => file.name).join(','), 'one.md,two.md');
  assert.equal(h.query.value, 'before  after');
  assert.equal(h.textarea.selectionStart, 7);
  assert.equal(h.showMention.value, false);
});

test('file draft confirmation from a previous context or closed menu cannot commit', () => {
  const h = harness();
  const files = [{ id: 'late', name: 'late.md', kbId: 'kb' }];
  h.apply({ contextKey: 'previous', files, replaceWholeKbIds: [] });
  h.showMention.value = false;
  h.apply({ contextKey: 'current', files, replaceWholeKbIds: [] });
  assert.equal(h.commits(), 0);
  assert.equal(h.query.value, 'before @alpha after');
});

for (const button of [false, true]) {
  test(`${button ? 'button' : 'typed @'} directory confirmation reuses file metadata, request IDs and caret handling`, () => {
    const h = harness(button);
    h.select({ id: 'doc', type: 'file', name: '说明书.pdf', kbId: 'kb', kbName: 'Library', folderPath: '产品' });
    h.flush();
    assert.deepEqual(h.ids, ['doc']); assert.deepEqual(h.kbs, []);
    assert.equal(h.map.doc, 'kb'); assert.equal(h.fileIdToKbId.value.doc, 'kb');
    assert.equal(h.fileList.value[0]?.name, '说明书.pdf');
    assert.equal(h.query.value, button ? 'before after' : 'before  after');
    assert.equal(h.textarea.selectionStart, 7); assert.equal(h.textarea.focused, true);
    assert.equal(h.showMention.value, false);
    const request = JSON.parse(JSON.stringify(h.request()));
    assert.deepEqual(request, { knowledge_ids: ['doc'] });
  });
}

test('late file confirmation after closing does not modify selection or input', () => {
  const h = harness(); h.showMention.value = false;
  h.select({ id: 'late', type: 'file', name: 'late.pdf', kbId: 'kb' });
  assert.deepEqual(h.ids, []); assert.deepEqual(h.map, {}); assert.equal(h.query.value, 'before @alpha after');
});

test('whole-KB selection retains its original immediate behavior', () => {
  const h = harness(); h.select({ id: 'kb', type: 'kb', name: 'Library' });
  assert.deepEqual(h.kbs, ['kb']); assert.deepEqual(h.ids, []);
  assert.deepEqual(JSON.parse(JSON.stringify(h.request())), { knowledge_base_ids: ['kb'] });
});

function fileRestoreHarness(
  queryBatch: Parameters<typeof restoreMentionFileNames>[3],
  sharedAgents: Array<{ agent: { id: string }; source_tenant_id: number }>,
  ids = ['file'],
) {
  const settingsStore = reactive({
    selectedAgentSourceTenantId: '12' as string | null,
    _legacySessionAgentId: null as string | null,
    resolveLegacySessionAgentSource(id: string, tenant: string) {
      if (this._legacySessionAgentId !== id) return;
      this.selectedAgentSourceTenantId = tenant;
      this._legacySessionAgentId = null;
    },
    reconcileRestoredFileKbMap(files: Array<{ id: string; kbId: string }>) {
      for (const file of files) fileIdToKbId.value[file.id] = file.kbId;
    },
  });
  const selectedAgentId = ref('local-agent');
  const selectedFileIds = ref(ids);
  const fileIdToKbId = ref(Object.fromEntries(ids.map(id => [id, 'kb'])));
  const fileList = ref<Array<{ id: string; name: string }>>([]);
  const props = reactive({ sessionId: 'previous-session' });
  const mentionAuth = reactive({ user: { id: 'user' }, selectedTenantId: 'current' });
  const sharedAgent = source.slice(source.indexOf('const selectedSharedAgent ='), source.indexOf('// 判断是否为自定义智能体'));
  const loader = source.slice(source.indexOf('const fileAgentSourceTenantId ='), source.indexOf('// A shared agent @mentions'));
  const watcherStart = source.search(/watch\(\[\r?\n\s+selectedFileIds, \(\) => selectedFileIds/);
  assert(watcherStart >= 0);
  const watcher = source.slice(watcherStart, source.indexOf('const isWebSearchConfigured ='));
  const scope = effectScope();
  scope.run(() => runInNewContext(ts.transpile(`${sharedAgent}\n${loader}\n${watcher}`), {
    settingsStore, selectedAgentId, selectedFileIds, fileIdToKbId, fileList, props, mentionAuth,
    computed, watch, orgStore: reactive({ sharedAgents }), agents: ref([]), isBuiltinAgent: () => false,
    AbortController, restoreMentionFileNames, batchQueryKnowledge: queryBatch,
  }));
  return { fileList, props, settingsStore, selectedAgentId, stop: () => scope.stop() };
}

test('file hydration ignores the unrelated browser default tenant for a local session', async (t) => {
  const calls: Array<{ agentId?: string; sourceTenantId?: string }> = [];
  const h = fileRestoreHarness(async (_query, _kbId, agentId, sourceTenantId) => {
    calls.push({ agentId, sourceTenantId });
    return { success: true, data: [{ id: 'file', title: 'Owned file.md', knowledge_base_id: 'kb' }] };
  }, [{ agent: { id: 'default-shared-agent' }, source_tenant_id: 12 }], ['file', 'missing']);
  t.after(h.stop);
  await new Promise<void>(resolve => setImmediate(resolve));
  assert.deepEqual(calls, Array.from({ length: 2 }, () => ({ agentId: undefined, sourceTenantId: undefined })));
  assert.equal(h.fileList.value[0]?.name, 'Owned file.md');
});

test('legacy shared history resolves the verified source into composer state for browse and send', async (t) => {
  const h = fileRestoreHarness(async (_query, _kbId, agentId) => ({
    success: true, data: agentId ? [{ id: 'file', title: 'Shared.md', knowledge_base_id: 'kb' }] : [],
  }), [{ agent: { id: 'legacy-shared-agent' }, source_tenant_id: 42 }]);
  t.after(h.stop);
  h.props.sessionId = 'legacy-session';
  h.selectedAgentId.value = 'legacy-shared-agent';
  h.settingsStore.selectedAgentSourceTenantId = null;
  h.settingsStore._legacySessionAgentId = 'legacy-shared-agent';
  await nextTick();
  await new Promise<void>(resolve => setImmediate(resolve));
  assert.equal(h.settingsStore.selectedAgentSourceTenantId, '42');
  assert.equal(h.fileList.value[0]?.name, 'Shared.md');
});

test('opening a different session cancels its predecessor and restores with the new verified shared scope', async (t) => {
  type BatchResult = Awaited<ReturnType<Parameters<typeof restoreMentionFileNames>[3]>>;
  const row = { id: 'file', knowledge_base_id: 'kb' };
  let resolveOld!: (value: BatchResult) => void;
  const oldReply = new Promise<BatchResult>(resolve => { resolveOld = resolve; });
  const calls: Array<{ agentId?: string; sourceTenantId?: string; signal?: AbortSignal }> = [];
  const h = fileRestoreHarness(async (_query, _kbId, agentId, sourceTenantId, signal) => {
    calls.push({ agentId, sourceTenantId, signal });
    if (calls.length === 1) return oldReply;
    return { success: true, data: agentId ? [{ ...row, title: 'Shared file.md' }] : [] };
  }, [{ agent: { id: 'restored-shared-agent' }, source_tenant_id: 42 }]);
  t.after(h.stop);

  h.props.sessionId = 'shared-session';
  h.selectedAgentId.value = 'restored-shared-agent';
  h.settingsStore.selectedAgentSourceTenantId = '42';
  await nextTick();
  await new Promise<void>(resolve => setImmediate(resolve));
  assert.equal(calls[0]?.signal?.aborted, true);
  assert.deepEqual(calls.map(({ agentId, sourceTenantId }) => [agentId, sourceTenantId]), [
    [undefined, undefined], [undefined, undefined], ['restored-shared-agent', '42'],
  ]);
  resolveOld({ success: true, data: [{ ...row, title: 'Old file.md' }] });
  await new Promise<void>(resolve => setImmediate(resolve));
  assert.deepEqual(Array.from(h.fileList.value, file => file.name), ['Shared file.md']);
});

for (const stage of ['kb', 'tags', 'capabilities', 'skills']) {
  test(`invalidating a mention during ${stage} loading does not restart file search or loading`, async () => {
    let release!: (value?: unknown) => void;
    const pending = new Promise(resolve => { release = resolve; });
    const calls: string[] = [];
    const pause = async (name: string, result: unknown) => {
      calls.push(name);
      if (name === stage) await pending;
      return result;
    };
    const loading = ref(false);
    const globals = {
      ref, console: { log() {}, error() {} }, mentionLoading: loading,
      mentionGroupCounts: ref({}), mentionOffset: ref(0), mentionItems: ref([]),
      mentionHasMore: ref(false), mentionActiveIndex: ref(0), mentionAllowedKbIds: ref(null),
      mentionBrowserKbs: ref([]), sharedAgentKbList: ref([]), sharedAgentOrgName: ref(''),
      settingsStore: {}, selectedAgentId: ref('agent'), knowledgeBases: ref([{ id: 'kb', name: 'Library' }]),
      orgStore: { sharedKnowledgeBases: [] }, hasAgentConfig: ref(false),
      agentKBSelectionMode: ref('all'), agentAllowedTools: ref([]), agentSupportedFileTypes: ref([]),
      toolsConsumeFiles: () => true,
      chatResources: { fetchKnowledgeBaseById: () => pause('kb', { knowledge_count: 1 }) },
      listKnowledgeTags: () => pause('tags', { data: [] }),
      agentMCPSelectionMode: ref('none'), agentSkillsSelectionMode: ref('all'),
      deploymentCapabilities: { ensureLoaded: () => pause('capabilities', undefined), isSupported: () => true },
      editorResources: { ensureSkills: () => pause('skills', undefined), skills: [] },
      hostSkillsOnly: () => true, mentionSkillTargetId: () => 'host', currentAgentConfig: ref({}), currentAgentScope: ref({}),
      searchKnowledge: async () => { calls.push('files'); return { data: [] }; },
      MENTION_PAGE_SIZE: 20,
    };
    const loader = source.slice(source.indexOf('const loadMentionItems ='), source.indexOf('const loadMoreMentionItems ='));
    const h = runInNewContext(ts.transpile(`let mentionLoadSeq = 0, lastMentionQuery = '';\n${loader}\n({load: loadMentionItems, invalidate: () => ++mentionLoadSeq})`), globals);
    const request = h.load('');
    // Allow the real loader to reach the selected asynchronous boundary.
    for (let turn = 0; turn < 20 && !calls.includes(stage); turn++) await Promise.resolve();
    assert(calls.includes(stage));
    h.invalidate();
    release();
    await request;
    assert.equal(calls.includes('files'), false);
    assert.equal(loading.value, false);
  });
}
