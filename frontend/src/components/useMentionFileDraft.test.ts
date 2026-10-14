import assert from 'node:assert/strict'
import test from 'node:test'
import { effectScope, reactive } from 'vue'
import type { MentionItem } from '../types/mention.ts'
import { useMentionFileDraft } from './useMentionFileDraft.ts'

const file = (id: string, kbId = 'kb-1'): MentionItem => ({
  id, type: 'file', name: 'Report', kbId, kbName: kbId, orgName: 'Shared space',
})

function harness(selectedFileIds: string[] = []) {
  const props = reactive({
    visible: true, selectedFileIds, fileSelectionScope: 'kb-1,kb-2',
    query: '', items: [] as MentionItem[], group: 'file',
  })
  const scope = effectScope()
  const draft = scope.run(() => useMentionFileDraft(props))!
  return { props, draft, stop: () => scope.stop() }
}

test('keeps complete file metadata across searches, group changes and pages', () => {
  const h = harness()
  try {
    const first = file('first')
    const second = file('second', 'kb-2')
    h.props.items = [first]
    h.draft.toggleFile(first)
    h.props.query = 'another search'
    h.props.group = 'root'
    h.props.items = []
    assert.equal(h.draft.isChecked(first.id), true)
    h.props.items = [second]
    h.draft.toggleFile(second)
    h.props.items = [second, file('next-page')]
    assert.equal(h.draft.pendingCount.value, 2)
    assert.deepEqual(h.draft.confirm(), [first, second])
    assert.deepEqual(h.props.selectedFileIds, [], 'draft changes do not commit settings')
  } finally { h.stop() }
})

test('toggles by file ID without duplicates and snapshots the selected metadata', () => {
  const h = harness()
  try {
    const original = file('first')
    h.draft.toggleFile(original)
    original.name = 'Changed outside the selector'
    assert.equal(h.draft.confirm()[0]!.name, 'Report')
    h.props.visible = false
    h.props.visible = true
    h.draft.toggleFile(file('first'))
    h.draft.toggleFile(file('first'))
    assert.equal(h.draft.pendingCount.value, 0)
    h.draft.toggleFile(file('first'))
    assert.equal(h.draft.pendingCount.value, 1)
  } finally { h.stop() }
})

test('marks committed files checked and disabled without adding them again', () => {
  const h = harness(['existing'])
  try {
    const existing = file('existing')
    assert.equal(h.draft.isChecked(existing.id), true)
    assert.equal(h.draft.isDisabled(existing), true)
    h.draft.toggleFile(existing)
    assert.equal(h.draft.pendingCount.value, 0)
    h.draft.toggleFile(file('new'))
    assert.deepEqual(h.draft.confirm().map(item => item.id), ['new'])
    assert.deepEqual(h.props.selectedFileIds, ['existing'])
  } finally { h.stop() }
})

test('reconciles externally selected files without losing other pending files', () => {
  const h = harness()
  try {
    h.draft.toggleFile(file('first'))
    h.draft.toggleFile(file('second'))
    h.props.selectedFileIds.push('first')
    assert.equal(h.draft.pendingCount.value, 1)
    assert.equal(h.draft.isDisabled(file('first')), true)
    assert.deepEqual(h.draft.confirm().map(item => item.id), ['second'])
  } finally { h.stop() }
})

test('blocks other resource types only while files are pending', () => {
  const h = harness()
  try {
    const others: MentionItem[] = ['kb', 'tag', 'mcp', 'skill'].map(type => ({
      id: type, name: type, type: type as MentionItem['type'],
    }))
    for (const item of others) assert.equal(h.draft.isDisabled(item), false)
    h.draft.toggleFile(file('first'))
    for (const item of others) {
      assert.equal(h.draft.isDisabled(item), true)
      h.draft.toggleFile(item)
    }
    assert.equal(h.draft.pendingCount.value, 1)
    h.draft.toggleFile(file('first'))
    for (const item of others) assert.equal(h.draft.isDisabled(item), false)
  } finally { h.stop() }
})

test('confirms once and does not accept more selections until the next opening', () => {
  const h = harness()
  try {
    assert.deepEqual(h.draft.confirm(), [])
    h.draft.toggleFile(file('first'))
    assert.deepEqual(h.draft.confirm().map(item => item.id), ['first'])
    assert.deepEqual(h.draft.confirm(), [])
    h.draft.toggleFile(file('second'))
    assert.equal(h.draft.pendingCount.value, 0)
    h.props.visible = false
    h.props.visible = true
    h.draft.toggleFile(file('second'))
    assert.deepEqual(h.draft.confirm().map(item => item.id), ['second'])
  } finally { h.stop() }
})

test('cancel and external close discard only the draft, including a rapid reopen', () => {
  const h = harness(['existing'])
  try {
    h.draft.toggleFile(file('first'))
    h.draft.cancel()
    assert.deepEqual(h.draft.confirm(), [])
    assert.deepEqual(h.props.selectedFileIds, ['existing'])
    h.props.visible = false
    h.props.visible = true
    h.draft.toggleFile(file('second'))
    h.props.visible = false
    h.props.visible = true
    assert.equal(h.draft.pendingCount.value, 0)
    h.draft.toggleFile(file('third'))
    assert.deepEqual(h.draft.confirm().map(item => item.id), ['third'])
  } finally { h.stop() }
})

test('a changed file scope clears the draft without closing or locking the menu', () => {
  const h = harness()
  try {
    h.draft.toggleFile(file('first'))
    h.props.fileSelectionScope = 'kb-1,kb-2'
    assert.equal(h.draft.pendingCount.value, 1)
    h.props.fileSelectionScope = 'kb-2'
    assert.equal(h.draft.pendingCount.value, 0)
    assert.equal(h.props.visible, true)
    h.draft.toggleFile(file('second', 'kb-2'))
    assert.deepEqual(h.draft.confirm().map(item => item.id), ['second'])
  } finally { h.stop() }
})
