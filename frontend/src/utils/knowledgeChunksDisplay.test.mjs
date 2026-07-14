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
