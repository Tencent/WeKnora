import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import { computed, effectScope, reactive, ref, watch } from 'vue'

const source = readFileSync(new URL('./Input-field.vue', import.meta.url), 'utf8')
const selectionCode = source.slice(source.indexOf('const addMentionItem ='), source.indexOf('const removeFile ='))

function composer({ button = false, allowed = null } = {}) {
  const query = ref(button ? 'before after' : 'before @alpha after')
  const selectedFileIds = ref(['existing'])
  const showMention = ref(true)
  const fileList = ref([{ id: 'existing', name: 'Existing' }])
  const fileIdToKbId = ref({ existing: 'kb-old' })
  const savedMap = { existing: 'kb-old' }
  const added = []
  const ticks = []
  const textarea = {
    selectionStart: button ? 7 : 13,
    selectionEnd: button ? 7 : 13,
    focusCount: 0,
    focus() { this.focusCount++ },
  }
  const settingsStore = {
    addFile(id) {
      // Selection observers must see the new file's label and KB immediately.
      assert.ok(fileList.value.some(file => file.id === id))
      assert.equal(fileIdToKbId.value[id], savedMap[id])
      if (!selectedFileIds.value.includes(id)) selectedFileIds.value.push(id)
      added.push(id)
    },
    setFileKbMap(updates) { Object.assign(savedMap, updates) },
    addKnowledgeBase(id) { added.push(`kb:${id}`) },
  }
  const handlers = vm.runInNewContext(ts.transpile(`${selectionCode}\n({ onMentionFilesSelect, onMentionSelect })`), {
    query, selectedFileIds, showMention, fileList, fileIdToKbId, settingsStore,
    mentionAllowedKbIds: ref(allowed), getTextareaEl: () => textarea,
    isMentionTriggeredByButton: ref(button), mentionStartPos: ref(7),
    nextTick: callback => ticks.push(callback),
    closeMentionFromPopup: () => { showMention.value = false; textarea.focus() },
  })
  return { ...handlers, query, selectedFileIds, showMention, fileList, savedMap, added, textarea,
    flush: () => ticks.splice(0).forEach(callback => callback()) }
}

const alpha = { id: 'a', name: 'Alpha', type: 'file', kbId: 'kb-a' }
const beta = { id: 'b', name: 'Beta', type: 'file', kbId: 'kb-b' }

test('file confirmation appends distinct cross-search references with metadata and removes the query once', () => {
  const c = composer()
  c.onMentionFilesSelect([alpha, beta, alpha, { ...alpha, id: 'existing' }])
  assert.deepEqual(c.selectedFileIds.value, ['existing', 'a', 'b'])
  assert.deepEqual(c.added, ['a', 'b'])
  assert.deepEqual(c.savedMap, { existing: 'kb-old', a: 'kb-a', b: 'kb-b' })
  assert.deepEqual(c.fileList.value.map(file => file.name), ['Existing', 'Alpha', 'Beta'])
  assert.equal(c.query.value, 'before  after')
  assert.equal(c.showMention.value, false)
  c.flush()
  assert.equal(c.textarea.selectionStart, 7)
  assert.equal(c.textarea.selectionEnd, 7)
  assert.equal(c.textarea.focusCount, 1)
  c.onMentionFilesSelect([beta])
  assert.equal(c.query.value, 'before  after', 'a duplicate event must not trim the draft again')
  assert.deepEqual(c.added, ['a', 'b'])
})

test('button-triggered selection preserves the conversation text and restores focus', () => {
  const c = composer({ button: true })
  c.onMentionFilesSelect([alpha, beta])
  c.flush()
  assert.equal(c.query.value, 'before after')
  assert.equal(c.textarea.focusCount, 1)
  assert.equal(c.textarea.selectionStart, 7)
})

test('confirmation cannot add files outside the current allowed KBs or select other resource types', () => {
  const c = composer({ allowed: new Set(['kb-a']) })
  c.onMentionFilesSelect([alpha, beta, { id: 'missing-kb', name: 'Unknown', type: 'file' },
    { id: 'kb-b', name: 'Library', type: 'kb' }])
  assert.deepEqual(c.added, ['a'])
  assert.deepEqual(c.savedMap, { existing: 'kb-old', a: 'kb-a' })
})

test('an event after cancellation leaves existing references and the typed query untouched', () => {
  const c = composer()
  c.showMention.value = false
  c.onMentionFilesSelect([alpha])
  assert.deepEqual(c.selectedFileIds.value, ['existing'])
  assert.equal(c.query.value, 'before @alpha after')
  assert.equal(c.textarea.focusCount, 0)
})

test('a batch with no eligible new file does not consume the typed mention', () => {
  const c = composer({ allowed: new Set() })
  c.onMentionFilesSelect([alpha, beta])
  assert.deepEqual(c.added, [])
  assert.equal(c.query.value, 'before @alpha after')
  assert.equal(c.showMention.value, false, 'do not leave an already confirmed selector locked open')
})

test('non-file resources keep the original immediate selection and single cleanup', () => {
  const c = composer()
  c.onMentionSelect({ id: 'kb-a', name: 'Library', type: 'kb' })
  c.flush()
  assert.deepEqual(c.added, ['kb:kb-a'])
  assert.equal(c.showMention.value, false)
  assert.equal(c.query.value, 'before  after')
})

function scopeHarness() {
  const scope = effectScope()
  const values = {
    selectedAgentId: ref('agent-a'), settingsStore: reactive({ selectedAgentSourceTenantId: null }),
    props: reactive({ sessionId: 'session-a' }), agentKBSelectionMode: ref('all'),
    agentKnowledgeBases: ref(['kb-a', 'kb-b']), agentMode: ref('smart-reasoning'),
    agentAllowedTools: ref(['search_knowledge', 'retrieve_knowledge']), agentSupportedFileTypes: ref([]),
    knowledgeBases: ref([{ id: 'kb-a' }, { id: 'kb-b' }]),
    orgStore: reactive({ sharedKnowledgeBases: [] }), isKbCompatibleWithAgent: () => true,
    mentionAllowedKbIds: ref(new Set(['kb-a', 'kb-b'])), showMention: ref(true),
    mentionItems: ref([alpha]), mentionGroupCounts: ref({ file: 1 }), mentionLoading: ref(true),
  }
  const keys = source.slice(source.indexOf('const mentionContextKey ='), source.indexOf('const loadMentionItems ='))
  const invalidation = source.slice(source.indexOf('const invalidateMentionRequest ='), source.indexOf('const onMentionSearchInput ='))
  const cleared = []
  const result = scope.run(() => vm.runInNewContext(ts.transpile(`
    let mentionLoadSeq = 0;
    let mentionSearchTimer = 123;
    ${keys}
    ${invalidation}
    ({ getSequence: () => mentionLoadSeq, getTimer: () => mentionSearchTimer, mentionFileSelectionScope })
  `), { ...values, computed, watch, clearTimeout: timer => cleared.push(timer) }))
  return { ...values, ...result, cleared, stop: () => scope.stop() }
}

test('equivalent resource sets and reordered configuration preserve the open draft context', () => {
  const s = scopeHarness()
  try {
    s.knowledgeBases.value = [{ id: 'kb-b' }, { id: 'kb-a' }]
    s.agentKnowledgeBases.value = ['kb-b', 'kb-a']
    s.agentAllowedTools.value = ['retrieve_knowledge', 'search_knowledge']
    s.mentionAllowedKbIds.value = new Set(['kb-b', 'kb-a'])
    assert.equal(s.showMention.value, true)
    assert.equal(s.getSequence(), 0)
    assert.equal(s.mentionFileSelectionScope.value, '["kb-a","kb-b"]')
  } finally { s.stop() }
})

for (const [name, change] of [
  ['agent', s => { s.selectedAgentId.value = 'agent-b' }],
  ['source tenant', s => { s.settingsStore.selectedAgentSourceTenantId = 'tenant-b' }],
  ['conversation', s => { s.props.sessionId = 'session-b' }],
  ['available KBs', s => { s.knowledgeBases.value = [{ id: 'kb-a' }] }],
  ['file restrictions', s => { s.agentSupportedFileTypes.value = ['pdf'] }],
]) {
  test(`changing ${name} closes the draft and invalidates in-flight loads and debounce work`, () => {
    const s = scopeHarness()
    try {
      change(s)
      assert.equal(s.showMention.value, false)
      assert.ok(s.getSequence() > 0)
      assert.deepEqual(s.cleared, [123])
      assert.equal(s.getTimer(), null)
      assert.equal(s.mentionLoading.value, false)
      assert.equal(s.mentionItems.value.length, 0)
      assert.equal(s.mentionAllowedKbIds.value, null)
    } finally { s.stop() }
  })
}
