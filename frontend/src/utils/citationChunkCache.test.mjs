import assert from 'node:assert/strict'
import test from 'node:test'
import {
  clearCitationChunkCache,
  getCitationChunkCache,
  makeCitationCacheKey,
  setCitationChunkCache,
} from './citationChunkCache.ts'

test('makeCitationCacheKey combines scope and chunkId', () => {
  assert.equal(makeCitationCacheKey('session-1', 'chunk-a'), 'session-1::chunk-a')
  assert.equal(
    makeCitationCacheKey('embed:ch:tok', 'chunk-b'),
    'embed:ch:tok::chunk-b',
  )
})

test('get/set are isolated per scope', () => {
  clearCitationChunkCache()
  setCitationChunkCache('session-1', 'chunk-a', { content: 'one' })
  setCitationChunkCache('session-2', 'chunk-a', { content: 'two' })

  assert.equal(getCitationChunkCache('session-1', 'chunk-a')?.content, 'one')
  assert.equal(getCitationChunkCache('session-2', 'chunk-a')?.content, 'two')
})

test('clearCitationChunkCache clears one scope or all', () => {
  clearCitationChunkCache()
  setCitationChunkCache('session-1', 'chunk-a', { content: 'one' })
  setCitationChunkCache('session-2', 'chunk-a', { content: 'two' })

  clearCitationChunkCache('session-1')
  assert.equal(getCitationChunkCache('session-1', 'chunk-a'), undefined)
  assert.equal(getCitationChunkCache('session-2', 'chunk-a')?.content, 'two')

  clearCitationChunkCache()
  assert.equal(getCitationChunkCache('session-2', 'chunk-a'), undefined)
})
