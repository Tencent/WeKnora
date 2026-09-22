import assert from 'node:assert/strict'
import test from 'node:test'

import { buildKnowledgeChunksPath } from './chunkPath'

test('omits chunk_type so the backend keeps its text-only default', () => {
  assert.equal(
    buildKnowledgeChunksPath('knowledge-1', 1, 25),
    '/api/v1/chunks/knowledge-1?page=1&page_size=25',
  )
})

test('passes an explicitly selected chunk type through as chunk_type', () => {
  assert.equal(
    buildKnowledgeChunksPath('knowledge-1', 2, 25, 'image_ocr'),
    '/api/v1/chunks/knowledge-1?page=2&page_size=25&chunk_type=image_ocr',
  )
})

test('encodes the chunk type value', () => {
  assert.equal(
    buildKnowledgeChunksPath('knowledge-1', 1, 25, 'image caption'),
    '/api/v1/chunks/knowledge-1?page=1&page_size=25&chunk_type=image+caption',
  )
})
