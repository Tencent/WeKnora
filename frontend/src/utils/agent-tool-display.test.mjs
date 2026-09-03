import assert from 'node:assert/strict'
import test from 'node:test'
import { getAgentToolIconName } from './agent-tool-icons.ts'
import {
  getKnowledgeSearchSummaryHtml,
  getQueryText,
  getRagPipelineStepTitle,
  getWikiPageText,
} from './agent-tool-display.ts'

const t = (key, params) => {
  if (key === 'agentStream.search.foundResultsFromFiles') {
    return `found ${params?.count} from ${params?.files} files`
  }
  if (key === 'agentStream.search.candidatesBelowThreshold') {
    return `matched ${params?.count}, none relevant`
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

test('getAgentToolIconName maps sandbox shell tools to the terminal icon', () => {
  assert.equal(getAgentToolIconName('shell_exec'), 'terminal')
})

test('getAgentToolIconName maps skill and sandbox file tools', () => {
  assert.equal(getAgentToolIconName('read_skill'), 'file')
  assert.equal(getAgentToolIconName('list_sandbox_files'), 'folder')
  assert.equal(getAgentToolIconName('read_sandbox_file'), 'file')
  assert.equal(getAgentToolIconName('execute_skill_script'), 'code')
})

test('getAgentToolIconName maps Wiki tools to semantic search and reading icons', () => {
  assert.equal(getAgentToolIconName('wiki_search'), 'search')
  assert.equal(getAgentToolIconName('wiki_read_page'), 'file-search')
  assert.equal(getAgentToolIconName('wiki_read_source_doc'), 'file-search')
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

test('getWikiPageText supports persisted slugs arrays', () => {
  assert.equal(
    getWikiPageText({ slugs: ['entity/知识助理', 'concept/API管理'] }),
    'entity/知识助理、concept/API管理',
  )
  assert.equal(getWikiPageText('{"slug":"index"}'), 'index')
})

test('getKnowledgeSearchSummaryHtml includes file count when present', () => {
  const html = getKnowledgeSearchSummaryHtml(t, {
    results: [{}, {}],
    kb_counts: { a: 1, b: 2 },
  })
  assert.match(html, /found <strong>2<\/strong> from <strong>2<\/strong> files/)
})

// Candidates that all fell below the relevance threshold never reached the
// answer, so the row must not read like a plain empty search: the difference
// points at the threshold rather than at the knowledge base.
test('getKnowledgeSearchSummaryHtml distinguishes filtered candidates from an empty search', () => {
  assert.match(
    getKnowledgeSearchSummaryHtml(t, { count: 0, candidate_count: 10 }),
    /matched <strong>10<\/strong>, none relevant/,
  )
  assert.equal(
    getKnowledgeSearchSummaryHtml(t, { count: 0, candidate_count: 0 }),
    'agentStream.search.noResults',
  )
})

test('getRagPipelineStepTitle uses query-aware search labels', () => {
  const title = getRagPipelineStepTitle(t, {
    tool_name: 'knowledge_search',
    pending: true,
    arguments: { query: '讯飞开放平台' },
  })
  assert.equal(title, 'searching 讯飞开放平台')
})

test('getRagPipelineStepTitle uses web labels when search_source is web', () => {
  const title = getRagPipelineStepTitle(t, {
    tool_name: 'knowledge_search',
    pending: false,
    success: true,
    arguments: { search_source: 'web' },
  })
  assert.equal(title, 'agentStream.toolStatus.webSearch')
})

test('getRagPipelineStepTitle uses attachment parsing labels', () => {
  assert.equal(
    getRagPipelineStepTitle(t, { tool_name: 'attachment_parsing', pending: true }),
    'agentStream.toolStatus.attachmentParsing',
  )
  assert.equal(
    getRagPipelineStepTitle(t, { tool_name: 'attachment_parsing', pending: false, success: true }),
    'agentStream.toolStatus.attachmentParsingDone',
  )
})
