import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { runInNewContext } from 'node:vm';
import ts from 'typescript';
import { ref } from 'vue';

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
  const fileIdToKbId = ref<Record<string, string>>({});
  const ids: string[] = [], kbs: string[] = [], map: Record<string, string> = {};
  const textarea = { selectionStart: button ? 7 : 13, selectionEnd: button ? 7 : 13, focused: false, focus() { this.focused = true; } };
  const ticks: Array<() => void> = [];
  const store = {
    settings: { selectedTags: [] },
    addFile(id: string) { if (!ids.includes(id)) ids.push(id); },
    addKnowledgeBase(id: string) { kbs.push(id); },
    setFileKbMap(updates: Record<string, string>) { Object.assign(map, updates); },
    getSelectedFiles: () => ids, getSelectedKnowledgeBases: () => kbs,
  };
  const globals = { query, showMention, fileList, fileIdToKbId, settingsStore: store,
    getTextareaEl: () => textarea, isMentionTriggeredByButton: ref(button), mentionStartPos: ref(7), nextTick: (fn: () => void) => ticks.push(fn) };
  const select = runInNewContext(ts.transpile(`${selection}\nonMentionSelect`), globals);
  const serialize = runInNewContext(ts.transpile(`({${params}})`)).getSuggestedQuestionsParams;
  return { select, query, showMention, fileList, fileIdToKbId, ids, kbs, map, textarea,
    flush: () => ticks.splice(0).forEach(fn => fn()), request: () => serialize.call(store) };
}

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
