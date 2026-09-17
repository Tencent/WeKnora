import assert from 'node:assert/strict'
import test from 'node:test'
import { effectScope, nextTick, ref } from 'vue'
import { useKnowledgeTagSelection, type KnowledgeTag } from './useKnowledgeTagSelection.ts'

function harness(createTag: Parameters<typeof useKnowledgeTagSelection>[0]['createTag'] = async () => {
  throw new Error('unexpected creation')
}) {
  const visible = ref(false)
  const kbId = ref('kb-a')
  const tags = ref<KnowledgeTag[]>([{ id: 'a', name: 'Alpha' }, { id: 'b', name: 'Beta' }])
  const selectedIds = ref(['a'])
  const errors: unknown[] = []
  let created = 0
  const scope = effectScope()
  const selection = scope.run(() => useKnowledgeTagSelection({
    visible: () => visible.value,
    kbId: () => kbId.value,
    tags: () => tags.value,
    selectedIds: () => selectedIds.value,
    createTag,
    onCreated: () => { created++ },
    onError: error => errors.push(error),
  }))!
  return { selection, visible, kbId, tags, selectedIds, errors, created: () => created, stop: () => scope.stop() }
}

test('opening hydrates selection and reopening discards edits using the latest parent values', async (t) => {
  const h = harness()
  t.after(h.stop)
  h.visible.value = true
  await nextTick()
  assert.deepEqual([...h.selection.selectedSet.value], ['a'])
  h.selection.toggleTag('b')
  h.selection.searchQuery.value = 'draft search'
  h.selection.newTagName.value = 'draft tag'
  h.selectedIds.value = ['b']
  await nextTick()
  assert.deepEqual([...h.selection.selectedSet.value], ['a', 'b'], 'parent refresh must not overwrite an open draft')
  h.visible.value = false
  await nextTick()
  h.visible.value = true
  await nextTick()
  assert.deepEqual([...h.selection.selectedSet.value], ['b'])
  assert.equal(h.selection.searchQuery.value, '')
  assert.equal(h.selection.newTagName.value, '')
})

test('search excludes selected tags, tracks refreshed names, and preserves IDs absent from the current list', async (t) => {
  const h = harness()
  t.after(h.stop)
  h.selectedIds.value = ['a', 'not-loaded']
  h.visible.value = true
  await nextTick()
  assert.deepEqual(h.selection.selectedTagsList.value.map(tag => tag.id), ['a'])
  assert.deepEqual([...h.selection.selectedSet.value], ['a', 'not-loaded'])
  h.selection.searchQuery.value = '  BETA  '
  assert.deepEqual(h.selection.availableTagsList.value.map(tag => tag.id), ['b'])
  h.selection.toggleTag('b')
  assert.deepEqual(h.selection.availableTagsList.value, [])
  h.selection.toggleTag('b')
  h.tags.value = [{ id: 'a', name: 'Renamed' }, { id: 'b', name: 'Gamma' }]
  assert.equal(h.selection.selectedTagsList.value[0]?.name, 'Renamed')
  assert.deepEqual(h.selection.availableTagsList.value, [])
  h.selection.searchQuery.value = ''
  h.selection.clearAll()
  assert.equal(h.selection.selectedSet.value.size, 0)
  assert.equal(h.selection.availableTagsList.value.length, 2)
})

test('search creation uses the current KB, selects the returned tag and clears only the search input', async (t) => {
  const requests: [string, { name: string }][] = []
  const h = harness(async (kbId, data) => {
    requests.push([kbId, data])
    return { data: { id: 'new', name: data.name } }
  })
  t.after(h.stop)
  h.kbId.value = 'kb-b'
  h.selection.searchQuery.value = ' New tag '
  h.selection.newTagName.value = 'separate draft'
  await h.selection.handleCreateTag()
  assert.deepEqual(requests, [['kb-b', { name: 'New tag' }]])
  assert.deepEqual([...h.selection.selectedSet.value], ['new'])
  assert.equal(h.selection.searchQuery.value, '')
  assert.equal(h.selection.newTagName.value, 'separate draft')
  assert.equal(h.created(), 1)
  assert.equal(h.selection.creatingTag.value, false)
  h.tags.value.push({ id: 'new', name: 'New tag' })
  assert.equal(h.selection.selectedTagsList.value[0]?.name, 'New tag')
})

test('manual entry reuses an exact existing name without creating or announcing a new tag', async (t) => {
  const h = harness()
  t.after(h.stop)
  h.selection.newTagName.value = ' Alpha '
  await h.selection.handleAddNewTag()
  assert.deepEqual([...h.selection.selectedSet.value], ['a'])
  assert.equal(h.selection.newTagName.value, '')
  assert.equal(h.created(), 0)
  assert.deepEqual(h.errors, [])
})

test('manual creation accepts an unwrapped response without clearing a separate search', async (t) => {
  const h = harness(async (_, data) => ({ id: 'new', name: data.name }))
  t.after(h.stop)
  h.selection.newTagName.value = 'New'
  h.selection.searchQuery.value = 'retained search'
  await h.selection.handleAddNewTag()
  assert.deepEqual([...h.selection.selectedSet.value], ['new'])
  assert.equal(h.selection.newTagName.value, '')
  assert.equal(h.selection.searchQuery.value, 'retained search')
  assert.equal(h.created(), 1)
})

test('failed creation preserves the draft and selection, releases loading and permits retry', async (t) => {
  let reject!: (error: Error) => void
  const h = harness(() => new Promise((_, rejectRequest) => { reject = rejectRequest }))
  t.after(h.stop)
  h.selection.toggleTag('a')
  h.selection.newTagName.value = 'retry me'
  const pending = h.selection.handleAddNewTag()
  assert.equal(h.selection.creatingTag.value, true)
  const failure = new Error('denied')
  reject(failure)
  await pending
  assert.equal(h.selection.creatingTag.value, false)
  assert.equal(h.selection.newTagName.value, 'retry me')
  assert.deepEqual([...h.selection.selectedSet.value], ['a'])
  assert.deepEqual(h.errors, [failure])
  assert.equal(h.created(), 0)
  const retry = h.selection.handleAddNewTag()
  assert.equal(h.selection.creatingTag.value, true)
  reject(failure)
  await retry
  assert.equal(h.errors.length, 2)
})

test('empty inputs do not create tags and dialogs keep independent selection state', async (t) => {
  const first = harness(), second = harness()
  t.after(first.stop)
  t.after(second.stop)
  first.selection.searchQuery.value = '  '
  first.selection.newTagName.value = '  '
  await first.selection.handleCreateTag()
  await first.selection.handleAddNewTag()
  assert.deepEqual(first.errors, [])
  first.selection.toggleTag('a')
  assert.equal(second.selection.selectedSet.value.size, 0)
})
