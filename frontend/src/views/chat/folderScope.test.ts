import assert from 'node:assert/strict'
import test from 'node:test'
import {
  collectFolderIdsFromMentions,
  buildFolderIdsField,
  partitionValidFolderIds,
} from './folderScope.ts'

test('collectFolderIdsFromMentions filters folder type and dedupes', () => {
  const items = [
    { type: 'kb', id: 'kb-1' },
    { type: 'folder', id: 'f-1' },
    { type: 'file', id: 'd-1' },
    { type: 'folder', id: 'f-1' }, // dup
    { type: 'folder', id: 'f-2' },
    { type: 'folder' }, // no id, skipped
  ]
  assert.deepEqual(collectFolderIdsFromMentions(items), ['f-1', 'f-2'])
})

test('buildFolderIdsField returns undefined when empty, else the array', () => {
  assert.equal(buildFolderIdsField([]), undefined)
  assert.deepEqual(buildFolderIdsField(['f-1']), ['f-1'])
})

test('partitionValidFolderIds splits known vs unknown', () => {
  const resolvable = new Set(['f-1', 'f-2'])
  const { valid, invalid } = partitionValidFolderIds(['f-1', 'f-3', 'f-2'], resolvable)
  assert.deepEqual(valid, ['f-1', 'f-2'])
  assert.deepEqual(invalid, ['f-3'])
})
