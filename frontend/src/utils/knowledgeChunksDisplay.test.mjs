import assert from 'node:assert/strict'
import test from 'node:test'

import { getKnowledgeChunksSummaryHtml } from './knowledgeChunksDisplay.ts'

const t = (key, params) => {
  if (key === 'agentStream.knowledgeChunksList.chunkRange') {
    return `loaded ${params?.fetched}/${params?.total}`
  }
  if (key === 'agentStream.knowledgeChunksList.page') {
    return `page ${params?.page}/${params?.pageSize}`
  }
  if (key === 'agentStream.knowledgeChunksList.offsetRange') {
    return `chunks ${params?.from}-${params?.to}`
  }
  if (key === 'agentStream.knowledgeChunksList.queryMatches') {
    return `found ${params?.count} for ${params?.query}`
  }
  if (key === 'agentStream.knowledgeChunksList.queryNoMatch') {
    return `none for ${params?.query}`
  }
  return key
}

test('getKnowledgeChunksSummaryHtml joins chunk range and page', () => {
  const html = getKnowledgeChunksSummaryHtml(t, {
    display_type: 'knowledge_chunks_list',
    fetched_chunks: 20,
    total_chunks: 282,
    page: 3,
    page_size: 20,
  })
  assert.match(html, /loaded <strong>20<\/strong>\/<strong>282<\/strong>/)
  assert.match(html, /page 3\/20/)
})

test('getKnowledgeChunksSummaryHtml reports in-document search results, not a chunk range', () => {
  const none = getKnowledgeChunksSummaryHtml(t, {
    display_type: 'knowledge_chunks_list',
    fetched_chunks: 0,
    total_chunks: 708,
    query: 'sunflower <f>',
    match_count: 0,
  })
  assert.equal(none, 'none for sunflower &lt;f&gt;')

  const some = getKnowledgeChunksSummaryHtml(t, {
    display_type: 'knowledge_chunks_list',
    fetched_chunks: 9,
    total_chunks: 708,
    query: 'sunflower',
    match_count: 20,
    truncated: true,
  })
  assert.equal(some, 'found <strong>20+</strong> for sunflower')
})

test('getKnowledgeChunksSummaryHtml shows the chunk range for offset paging', () => {
  const html = getKnowledgeChunksSummaryHtml(t, {
    display_type: 'knowledge_chunks_list',
    fetched_chunks: 37,
    total_chunks: 708,
    offset: 100,
    page: 2,
    page_size: 100,
  })
  assert.equal(html, 'loaded <strong>37</strong>/<strong>708</strong> · chunks 101-137')
})
