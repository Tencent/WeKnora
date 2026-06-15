import assert from 'node:assert/strict'
import test from 'node:test'

import { countGrepDocuments, groupGrepChunkResults } from './grepResultsGroup.ts'

test('groupGrepChunkResults merges chunks from the same document', () => {
  const grouped = groupGrepChunkResults([
    {
      chunk_id: 'chunk-a',
      knowledge_id: 'doc-1',
      knowledge_base_id: 'kb-1',
      knowledge_title: 'sample-report.pdf',
      match_snippet: 'hit one',
    },
    {
      chunk_id: 'chunk-b',
      knowledge_id: 'doc-1',
      knowledge_base_id: 'kb-1',
      knowledge_title: 'sample-report.pdf',
      match_snippet: 'hit two',
    },
    {
      chunk_id: 'chunk-c',
      knowledge_id: 'doc-2',
      knowledge_base_id: 'kb-1',
      knowledge_title: 'other-report.pdf',
      match_snippet: 'other hit',
    },
  ])

  assert.equal(grouped.length, 2)
  assert.equal(grouped[0].chunk_hit_count, 2)
  assert.equal(grouped[0].chunks.length, 2)
  assert.equal(grouped[1].chunk_hit_count, 1)
})

test('groupGrepChunkResults keeps FAQ entries separate', () => {
  const grouped = groupGrepChunkResults([
    {
      chunk_id: 'faq-1',
      faq_id: 'faq-1',
      knowledge_id: 'doc-faq',
      knowledge_base_id: 'kb-1',
      knowledge_title: 'FAQ doc',
      chunk_type: 'faq',
      faq_question: 'Question A',
      match_snippet: 'answer a',
    },
    {
      chunk_id: 'faq-2',
      faq_id: 'faq-2',
      knowledge_id: 'doc-faq',
      knowledge_base_id: 'kb-1',
      knowledge_title: 'FAQ doc',
      chunk_type: 'faq',
      faq_question: 'Question B',
      match_snippet: 'answer b',
    },
  ])

  assert.equal(grouped.length, 2)
  assert.equal(grouped[0].title, 'Question A')
  assert.equal(grouped[1].title, 'Question B')
})

test('countGrepDocuments prefers document_count from backend', () => {
  assert.equal(
    countGrepDocuments({
      document_count: 1,
      knowledge_results: [{ knowledge_id: 'doc-1' }, { knowledge_id: 'doc-2' }],
      chunk_results: [{ chunk_id: 'c1', knowledge_id: 'doc-1', knowledge_base_id: 'kb', knowledge_title: 'a' }],
    }),
    1,
  )
})

test('countGrepDocuments prefers knowledge_results length when document_count absent', () => {
  assert.equal(
    countGrepDocuments({
      knowledge_results: [{ knowledge_id: 'doc-1' }, { knowledge_id: 'doc-2' }],
      chunk_results: [{ chunk_id: 'c1', knowledge_id: 'doc-1', knowledge_base_id: 'kb', knowledge_title: 'a' }],
    }),
    2,
  )
})
