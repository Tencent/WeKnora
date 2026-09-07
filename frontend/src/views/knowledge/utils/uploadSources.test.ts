import assert from 'node:assert/strict'
import test from 'node:test'

import { chunkFolderFinalizeKnowledgeIDs, resolveFolderFileMode } from './folderFileMode.ts'

test('resolveFolderFileMode keeps default Markdown parseable while engines load', () => {
  assert.equal(resolveFolderFileMode('README.md', new Set()), 'defer-processing')
})

test('resolveFolderFileMode distinguishes configured parser files and attachments', () => {
  assert.equal(resolveFolderFileMode('README.md', new Set(['md'])), 'defer-processing')
  assert.equal(resolveFolderFileMode('diagram.svg', new Set(['md'])), 'store-only')
  assert.equal(resolveFolderFileMode('payload.exe', new Set(['md'])), null)
})

test('chunkFolderFinalizeKnowledgeIDs keeps every request within the API limit', () => {
  const ids = Array.from({ length: 1201 }, (_, index) => String(index))
  const batches = chunkFolderFinalizeKnowledgeIDs(ids)
  assert.deepEqual(batches.map(batch => batch.length), [1000, 201])
  assert.equal(batches.flat().length, ids.length)
})
