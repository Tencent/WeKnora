import assert from 'node:assert/strict'
import test from 'node:test'
import { getAgentToolIconName } from './agent-tool-icons.ts'
import {
  getKnowledgeSearchSummaryHtml,
  getQueryText,
  getRagPipelineStepTitle,
} from './agent-tool-display.ts'

const t = (key, params) => {
  if (key === 'agentStream.search.foundResultsFromFiles') {
    return `found ${params?.count} from ${params?.files} files`
  }
  if (key === 'agentStream.ragPipeline.searchingWithQuery') {
    return `searching ${params?.query}`
  }
  return key
}

test('getAgentToolIconName maps rag pipeline tools', () => {
  assert.equal(getAgentToolIconName('query_understand'), 'ai-search')
  assert.equal(getAgentToolIconName('knowledge_search'), 'data-search')
})

test('getQueryText joins unique query strings', () => {
  assert.equal(getQueryText({ query: 'foo', queries: ['foo', 'bar'] }), 'foo，bar')
})

test('getQueryText parses JSON-encoded queries string', () => {
  assert.equal(
    getQueryText({
      queries: '["合力天胜游泳俱乐部介绍", "合力天胜游泳训练机构", "合力天胜游泳队"]',
    }),
    '合力天胜游泳俱乐部介绍，合力天胜游泳训练机构，合力天胜游泳队',
  )
})

test('getKnowledgeSearchSummaryHtml includes file count when present', () => {
  const html = getKnowledgeSearchSummaryHtml(t, {
    results: [{}, {}],
    kb_counts: { a: 1, b: 2 },
  })
  assert.match(html, /found <strong>2<\/strong> from <strong>2<\/strong> files/)
})

test('getRagPipelineStepTitle uses query-aware search labels', () => {
  const title = getRagPipelineStepTitle(t, {
    tool_name: 'knowledge_search',
    pending: true,
    arguments: { query: '讯飞开放平台' },
  })
  assert.equal(title, 'searching 讯飞开放平台')
})
