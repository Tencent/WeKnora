import { test } from 'node:test'
import assert from 'node:assert/strict'
import { resolveCitationChunkId } from './citationMarkdown'

const refs = [
  {
    id: '11111111-1111-1111-1111-111111111111',
    knowledge_title: 'Alpha Guide',
    knowledge_filename: 'alpha.pdf',
    knowledge_base_id: 'kb-1',
    chunk_index: 0,
  },
  {
    id: '22222222-2222-2222-2222-222222222222',
    knowledge_title: 'Beta Notes',
    knowledge_filename: 'beta.pdf',
    knowledge_base_id: 'kb-2',
    chunk_index: 1,
  },
]

test('known UUID from refs passes through', () => {
  assert.equal(
    resolveCitationChunkId('22222222-2222-2222-2222-222222222222', {}, refs),
    '22222222-2222-2222-2222-222222222222',
  )
})

test('UUID comparison is case-insensitive', () => {
  assert.equal(
    resolveCitationChunkId('11111111-1111-1111-1111-111111111111'.toUpperCase(), {}, refs),
    '11111111-1111-1111-1111-111111111111'.toUpperCase(),
  )
})

test('hallucinated UUID falls back to doc match (issue #1323)', () => {
  assert.equal(
    resolveCitationChunkId(
      'deadbeef-0000-0000-0000-000000000000',
      { doc: 'Alpha Guide' },
      refs,
    ),
    '11111111-1111-1111-1111-111111111111',
  )
})

test('hallucinated UUID falls back to unique kb match', () => {
  assert.equal(
    resolveCitationChunkId(
      'deadbeef-0000-0000-0000-000000000000',
      { kbId: 'kb-2' },
      refs,
    ),
    '22222222-2222-2222-2222-222222222222',
  )
})

test('unknown UUID with no recovery hint returns raw (unchanged behavior)', () => {
  assert.equal(
    resolveCitationChunkId('deadbeef-0000-0000-0000-000000000000', {}, refs),
    'deadbeef-0000-0000-0000-000000000000',
  )
})

test('UUID passes through when refs are unavailable (streaming start)', () => {
  assert.equal(
    resolveCitationChunkId('deadbeef-0000-0000-0000-000000000000', {}),
    'deadbeef-0000-0000-0000-000000000000',
  )
  assert.equal(
    resolveCitationChunkId('deadbeef-0000-0000-0000-000000000000', {}, null),
    'deadbeef-0000-0000-0000-000000000000',
  )
})

test('web_search refs are ignored for UUID validation', () => {
  assert.equal(
    resolveCitationChunkId('deadbeef-0000-0000-0000-000000000000', { doc: 'Alpha Guide' }, [
      {
        id: 'deadbeef-0000-0000-0000-000000000000',
        chunk_type: 'web_search',
      },
      ...refs,
    ]),
    '11111111-1111-1111-1111-111111111111',
  )
})

test('numeric index mapping still works after UUID guard', () => {
  assert.equal(resolveCitationChunkId('1', {}, refs), '11111111-1111-1111-1111-111111111111')
  assert.equal(resolveCitationChunkId('2', {}, refs), '22222222-2222-2222-2222-222222222222')
})
