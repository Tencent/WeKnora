import assert from 'node:assert/strict'
import test from 'node:test'

import {
  appendKnowledgeFolderListQuery,
  readKnowledgeFolderSelection,
  resolveKnowledgeUploadFolderID,
  writeKnowledgeFolderSelection,
} from './knowledgeFolderState.ts'

test('reads all-files, root, and concrete-folder route query states distinctly', () => {
  assert.equal(readKnowledgeFolderSelection({}), undefined)
  assert.equal(readKnowledgeFolderSelection({ folder_id: '' }), '')
  assert.equal(readKnowledgeFolderSelection({ folder_id: 'folder-1' }), 'folder-1')
  assert.equal(readKnowledgeFolderSelection({ folder_id: ['folder-2', 'folder-3'] }), 'folder-2')
  assert.equal(readKnowledgeFolderSelection({ folder_id: null }), undefined)
})

test('writes folder selection without losing unrelated route query values', () => {
  const original = { tab: 'documents', keyword: 'guide', folder_id: 'folder-old' }

  assert.deepEqual(writeKnowledgeFolderSelection(original, 'folder-new'), {
    tab: 'documents',
    keyword: 'guide',
    folder_id: 'folder-new',
  })
  assert.deepEqual(writeKnowledgeFolderSelection(original, ''), {
    tab: 'documents',
    keyword: 'guide',
    folder_id: '',
  })
  assert.deepEqual(writeKnowledgeFolderSelection(original, undefined), {
    tab: 'documents',
    keyword: 'guide',
  })
  assert.deepEqual(original, { tab: 'documents', keyword: 'guide', folder_id: 'folder-old' })
})

test('resolves all-files and explicit root uploads to the root folder id', () => {
  assert.equal(resolveKnowledgeUploadFolderID(undefined), '')
  assert.equal(resolveKnowledgeUploadFolderID(''), '')
  assert.equal(resolveKnowledgeUploadFolderID('folder-1'), 'folder-1')
})

test('updates list folder_id across concrete, root, and all-files selections', () => {
  const searchParams = new URLSearchParams({ page: '1', page_size: '35' })

  appendKnowledgeFolderListQuery(searchParams, 'folder-1')
  assert.equal(searchParams.get('folder_id'), 'folder-1')

  appendKnowledgeFolderListQuery(searchParams, '')
  assert.equal(searchParams.has('folder_id'), true)
  assert.equal(searchParams.get('folder_id'), '')

  appendKnowledgeFolderListQuery(searchParams, undefined)
  assert.equal(searchParams.has('folder_id'), false)
  assert.equal(searchParams.get('page'), '1')
  assert.equal(searchParams.get('page_size'), '35')
})
