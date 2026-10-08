import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import { computed, effectScope, nextTick, reactive, ref, watch } from 'vue'

const source = readFileSync(new URL('./ChatArtifactsPanel.vue', import.meta.url), 'utf8')
const start = source.indexOf('function applyFocus(')
const end = source.indexOf('\nwatch(', start)
assert.ok(start >= 0 && end > start)
const applyFocusSource = ts.transpile(source.slice(start, end))

function setup({ top = 120, bottom = 180, left = 1600 } = {}) {
  const pending = []
  const scrolls = []
  const target = { messageId: 'target', index: 0 }
  const row = {
    getAttribute: () => 'target',
    getBoundingClientRect: () => ({ top, bottom, left, right: left + 400 }),
    scrollIntoView: () => assert.fail('Artifact focus must not scroll outer ancestors'),
  }
  const list = {
    scrollTop: 200,
    getBoundingClientRect: () => ({ top: 100, bottom: 400, left, right: left + 420 }),
    querySelectorAll: () => [row],
    scrollTo: ({ top }) => scrolls.push(top),
  }
  const previewItem = { value: null }
  const focusedMessageId = { value: null }
  const applyFocus = vm.runInNewContext(`${applyFocusSource}; applyFocus`, {
    listRef: { value: list }, previewItem, focusedMessageId,
    nextTick: (fn) => pending.push(fn),
    findItem: () => target,
  })
  return {
    applyFocus, scrolls, previewItem, focusedMessageId, target,
    flush: () => pending.splice(0).forEach((fn) => fn()),
  }
}

test('folder focus during panel entry locates a visible file without scrolling ancestors', () => {
  const ctx = setup()
  ctx.applyFocus({ messageId: 'target', nonce: 1 })
  ctx.flush()
  assert.equal(ctx.focusedMessageId.value, 'target')
  assert.deepEqual(ctx.scrolls, [])
})

test('folder focus reveals files above or below the list using only list scrolling', () => {
  for (const [top, bottom, expected] of [[40, 100, 140], [380, 440, 240]]) {
    const ctx = setup({ top, bottom })
    ctx.applyFocus({ messageId: 'target', nonce: 1 })
    ctx.flush()
    assert.deepEqual(ctx.scrolls, [expected])
  }
})

test('folder entry exits a preview and repeated clicks locate the file again', () => {
  const ctx = setup({ top: 380, bottom: 440 })
  ctx.applyFocus({ messageId: 'target', previewIndex: 0, nonce: 1 })
  assert.equal(ctx.previewItem.value, ctx.target)
  ctx.flush()
  assert.deepEqual(ctx.scrolls, [])
  for (const nonce of [2, 3]) {
    ctx.applyFocus({ messageId: 'target', nonce })
    ctx.flush()
    assert.equal(ctx.previewItem.value, null)
  }
  assert.deepEqual(ctx.scrolls, [240, 240])
})

test('header entry without a message focus does not scroll the artifact list', () => {
  const ctx = setup()
  ctx.applyFocus(null)
  ctx.flush()
  assert.deepEqual(ctx.scrolls, [])
  assert.equal(ctx.focusedMessageId.value, null)
})

const stateStart = source.indexOf('const listRef = ')
const stateEnd = source.indexOf('async function handleDownload(', stateStart)
assert.ok(stateStart >= 0 && stateEnd > stateStart)
const stateSource = ts.transpile(source.slice(stateStart, stateEnd))

function setupFilters(t) {
  const effects = effectScope()
  t.after(() => effects.stop())
  const props = reactive({
    sessionId: 'session-a',
    items: [
      { messageId: 'a', index: 0, file_name: 'Report.docx' },
      { messageId: 'b', index: 3, file_name: 'Report.docx' },
      { messageId: 'b', index: 7, file_name: 'chart.png' },
    ],
  })
  const panel = {
    artifactFocus: ref(null),
    clearArtifactFocus() { this.artifactFocus.value = null },
  }
  const state = effects.run(() => vm.runInNewContext(`${stateSource}; ({
    scope, searchQuery, currentItems, visibleItems, previewItem, openPreview, closePreview,
  })`, {
    computed, nextTick, reactive, ref, watch, props, panel,
    resolveFilePreviewExt: () => '',
  }))
  return { ...state, props, panel }
}

test('header entry shows all files; folder entry filters by message without changing artifact indexes', async (t) => {
  const ctx = setupFilters(t)
  assert.equal(ctx.scope.value, 'all')
  assert.equal(ctx.visibleItems.value.length, 3)
  ctx.panel.artifactFocus.value = { messageId: 'b', nonce: 1 }
  await nextTick()
  assert.equal(ctx.scope.value, 'current')
  assert.deepEqual(ctx.visibleItems.value.map((item) => item.index), [3, 7])
  ctx.scope.value = 'all'
  assert.equal(ctx.visibleItems.value.length, 3)
})

test('filename search combines with scope and survives incoming files', async (t) => {
  const ctx = setupFilters(t)
  ctx.panel.artifactFocus.value = { messageId: 'b', nonce: 1 }
  await nextTick()
  ctx.searchQuery.value = ' REPORT.DOCX '
  assert.equal(ctx.visibleItems.value.length, 1)
  ctx.scope.value = 'all'
  assert.equal(ctx.visibleItems.value.length, 2)
  ctx.props.items = [...ctx.props.items, { messageId: 'c', index: 0, file_name: 'Report.docx' }]
  await nextTick()
  assert.equal(ctx.scope.value, 'all')
  assert.equal(ctx.searchQuery.value, ' REPORT.DOCX ')
  assert.equal(ctx.visibleItems.value.length, 3)
  ctx.searchQuery.value = 'missing'
  assert.equal(ctx.visibleItems.value.length, 0)
})

test('switching message focus or session resets stale filters', async (t) => {
  const ctx = setupFilters(t)
  ctx.searchQuery.value = 'chart'
  ctx.panel.artifactFocus.value = { messageId: 'a', nonce: 1 }
  await nextTick()
  assert.equal(ctx.searchQuery.value, '')
  assert.equal(ctx.visibleItems.value[0].messageId, 'a')
  ctx.props.sessionId = 'session-b'
  await nextTick()
  assert.equal(ctx.scope.value, 'all')
  assert.equal(ctx.panel.artifactFocus.value, null)
})

test('preview returns to the selected scope and search', async (t) => {
  const ctx = setupFilters(t)
  ctx.panel.artifactFocus.value = { messageId: 'b', nonce: 1 }
  await nextTick()
  ctx.searchQuery.value = 'png'
  ctx.openPreview(ctx.visibleItems.value[0])
  assert.equal(ctx.previewItem.value.messageId, 'b')
  assert.equal(ctx.previewItem.value.index, 7)
  ctx.closePreview()
  assert.equal(ctx.scope.value, 'current')
  assert.equal(ctx.searchQuery.value, 'png')
  assert.equal(ctx.visibleItems.value.length, 1)
})
