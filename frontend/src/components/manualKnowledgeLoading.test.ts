import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test, { type TestContext } from 'node:test'
import * as pinia from 'pinia'
import { marked } from 'marked'
import { fixture, flush } from '../test-utils/vueFixture.ts'
import * as manualDraft from '../utils/manualKnowledgeDraft.ts'
import * as markdownEditing from '../utils/markdownEditing.ts'
import * as sidebarWidth from '../utils/sidebarWidth.ts'

function deferred<T = any>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
function detail(id: string) {
  return { data: { id, knowledge_base_id: 'kb', title: `Title ${id}`, parse_status: 'draft',
    metadata: { content: `Content ${id}`, status: 'draft', updated_at: `Time ${id}` }, tags: [{ id: `tag-${id}` }] } }
}
function setup(t: TestContext, api: Record<string, unknown> = {}, ensure = async () => {}) {
  const f = fixture(t)
  pinia.setActivePinia(pinia.createPinia())
  const { useUIStore } = f.evaluate(readFileSync(new URL('../stores/ui.ts', import.meta.url), 'utf8'), {
    pinia, '../utils/sidebarWidth': sidebarWidth,
  })
  const ui = useUIStore()
  const messages: string[] = []
  const writes: any[] = []
  const page = f.component(new URL('./manual-knowledge-editor.vue', import.meta.url), {
    marked: { marked },
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    'tdesign-vue-next': { DialogPlugin: { confirm: () => ({ destroy() {} }) }, MessagePlugin: {
      success: (s: string) => messages.push(s), error: (s: string) => messages.push(s), warning: (s: string) => messages.push(s),
    } },
    '@/utils/unsavedChanges': { registerUnsavedChanges: () => () => {} },
    '@/stores/ui': { useUIStore: () => ui },
    '@/utils/manualKnowledgeDraft': manualDraft,
    '@/api/knowledge-base': {
      getKnowledgeDetails: async (id: string) => detail(id),
      updateManualKnowledge: async (id: string, payload: unknown) => { writes.push({ id, payload }); return { success: true } },
      ...api,
    },
    '@/stores/uploadConfirm': { useUploadConfirmStore: () => ({}) },
    '@/stores/organization': { useOrganizationStore: () => ({ sharedKnowledgeBases: [] }) },
    '@/stores/chatResources': { useChatResourcesStore: () => ({
      ensureKnowledgeBases: ensure, rawKnowledgeBases: [{ id: 'kb', name: 'Documents', type: 'document' }],
    }) },
    '@/utils/security': { sanitizeHTML: (s: string) => s, safeMarkdownToHTML: (s: string) => s, hydrateProtectedFileImages: async () => {} },
    '@/utils/markdownEditing': markdownEditing,
  })
  async function open(id: string) {
    ui.openManualEditor({ mode: 'edit', knowledgeId: id, kbId: 'kb' })
    await flush()
  }
  async function hide() { ui.closeManualEditor(); await flush() }
  return { ...page, ui, messages, writes, open, hide }
}

test('a late document response cannot replace the current document then save its content to the wrong ID', async t => {
  const old = deferred()
  const p = setup(t, { getKnowledgeDetails: (id: string) => id === 'a' ? old.promise : Promise.resolve(detail(id)) })
  await p.open('a')
  await p.hide()
  await p.open('b')
  assert.equal(p.vm.form.content, 'Content b')
  old.resolve(detail('a'))
  await flush()
  const displayed = { title: p.vm.form.title, updated: p.vm.lastUpdatedAt }
  await p.vm.handleSave('draft')
  assert.equal(p.writes[0].id, 'b')
  assert.equal(p.writes[0].payload.content, 'Content b')
  assert.deepEqual([...p.writes[0].payload.tag_ids], ['tag-b'])
  assert.deepEqual(displayed, { title: 'Title b', updated: 'Time b' })
})

for (const outcome of ['success', 'failure'] as const) {
  test(`an old ${outcome} cannot unlock a new document still loading`, async t => {
    const a = deferred(), b = deferred()
    const p = setup(t, { getKnowledgeDetails: (id: string) => id === 'a' ? a.promise : b.promise })
    await p.open('a')
    await p.hide()
    await p.open('b')
    if (outcome === 'success') a.resolve(detail('a'))
    else a.reject(new Error('Old load failed'))
    await flush()
    assert.equal(p.vm.initialLoaded, false)
    assert.equal(p.vm.contentLoading, true)
    assert.deepEqual(p.messages, [])
    b.resolve(detail('b'))
    await flush()
    assert.equal(p.vm.initialLoaded, true)
    assert.equal(p.vm.form.content, 'Content b')
  })
}

test('a slow knowledge-base list from a closed visit cannot restart document loading', async t => {
  const old = deferred()
  let lists = 0
  const ids: string[] = []
  const p = setup(t, { getKnowledgeDetails: async (id: string) => { ids.push(id); return detail(id) } },
    () => ++lists === 1 ? old.promise : Promise.resolve())
  await p.open('a')
  await p.hide()
  await p.open('b')
  p.vm.form.content = 'Unsaved B changes'
  old.resolve(undefined)
  await flush()
  assert.deepEqual(ids, ['b'])
  assert.equal(p.vm.form.content, 'Unsaved B changes')
  assert.equal(p.vm.isDirty(), true)
})

test('A to B to A rejects the first visit response even with the same document ID', async t => {
  const old = deferred()
  let aCalls = 0
  const p = setup(t, { getKnowledgeDetails: (id: string) => id === 'a' && ++aCalls === 1 ? old.promise : Promise.resolve(detail(id)) })
  await p.open('a'); await p.hide(); await p.open('b'); await p.hide(); await p.open('a')
  p.vm.form.content = 'Latest A draft'
  old.resolve(detail('a'))
  await flush()
  assert.equal(p.vm.form.content, 'Latest A draft')
  assert.equal(p.vm.isDirty(), true)
})

test('unmount ignores pending content reads and their errors', async t => {
  const request = deferred()
  const p = setup(t, { getKnowledgeDetails: () => request.promise })
  await p.open('a')
  p.close()
  request.reject(new Error('Unmounted request'))
  await flush()
  assert.deepEqual(p.messages, [])
  assert.equal(p.vm.initialLoaded, false)
})

test('a late edit read cannot overwrite a new manual draft seeded from chat', async t => {
  const request = deferred()
  const p = setup(t, { getKnowledgeDetails: () => request.promise })
  await p.open('a')
  await p.hide()
  p.ui.openManualEditor({ mode: 'create', kbId: 'kb', title: 'Chat answer', content: 'Saved chat answer' })
  await flush()
  p.vm.form.content = 'Edited chat answer'
  request.resolve(detail('a'))
  await flush()
  assert.equal(p.vm.currentKnowledgeId, null)
  assert.equal(p.vm.form.title, 'Chat answer')
  assert.equal(p.vm.form.content, 'Edited chat answer')
  assert.equal(p.vm.isDirty(), true)
})

test('a closed load cannot mark the hidden editor initialized', async t => {
  const request = deferred()
  const p = setup(t, { getKnowledgeDetails: () => request.promise })
  await p.open('a')
  await p.hide()
  request.resolve(detail('a'))
  await flush()
  assert.equal(p.vm.initialLoaded, false)
  assert.equal(p.vm.form.content, '')
  assert.equal(p.vm.contentLoading, false)
})

test('a current detail error is still reported', async t => {
  const p = setup(t, { getKnowledgeDetails: async () => { throw new Error('Current failure') } })
  await p.open('a')
  assert.deepEqual(p.messages, ['manualEditor.error.fetchDetailFailed'])
  assert.equal(p.vm.contentLoading, false)
})
