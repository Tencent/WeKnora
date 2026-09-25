import assert from 'node:assert/strict'
import test from 'node:test'
import {
  allowedToolsInclude,
  normalizeLegacyToolName,
  normalizeLegacyToolNames,
} from './legacy-tool-names.ts'

test('normalizeLegacyToolName maps retired retrieval tools to their successors', () => {
  assert.equal(normalizeLegacyToolName('knowledge_search'), 'search_knowledge')
  assert.equal(normalizeLegacyToolName('grep_chunks'), 'search_knowledge')
  assert.equal(normalizeLegacyToolName('list_knowledge_chunks'), 'read_document')
  assert.equal(normalizeLegacyToolName('get_document_info'), 'read_document')
  assert.equal(normalizeLegacyToolName('wiki_read_source_doc'), 'read_document')
})

test('normalizeLegacyToolName leaves current and unknown names untouched', () => {
  assert.equal(normalizeLegacyToolName('search_knowledge'), 'search_knowledge')
  assert.equal(normalizeLegacyToolName('list_documents'), 'list_documents')
  assert.equal(normalizeLegacyToolName('wiki_read_page'), 'wiki_read_page')
  assert.equal(normalizeLegacyToolName('mcp_foo_bar'), 'mcp_foo_bar')
})

test('normalizeLegacyToolNames rewrites an old default RAG tool list and dedupes', () => {
  assert.deepEqual(
    normalizeLegacyToolNames(['thinking', 'grep_chunks', 'knowledge_search', 'list_knowledge_chunks', 'get_document_info', 'query_knowledge_graph']),
    ['thinking', 'search_knowledge', 'read_document', 'query_knowledge_graph'],
  )
})

test('normalizeLegacyToolNames keeps order and does not duplicate already-migrated names', () => {
  assert.deepEqual(
    normalizeLegacyToolNames(['search_knowledge', 'knowledge_search', 'wiki_search', 'wiki_read_source_doc', 'read_document']),
    ['search_knowledge', 'wiki_search', 'read_document'],
  )
})

test('normalizeLegacyToolNames returns a fresh array and tolerates junk input', () => {
  const input = ['thinking']
  const out = normalizeLegacyToolNames(input)
  assert.deepEqual(out, ['thinking'])
  assert.notEqual(out, input)
  assert.deepEqual(normalizeLegacyToolNames(undefined), [])
  assert.deepEqual(normalizeLegacyToolNames(null), [])
  assert.deepEqual(normalizeLegacyToolNames([]), [])
  assert.deepEqual(normalizeLegacyToolNames(['', '  ', 42, null, ' todo_write ']), ['todo_write'])
})

test('allowedToolsInclude matches by current or retired name', () => {
  assert.equal(allowedToolsInclude(['knowledge_search'], 'search_knowledge'), true)
  assert.equal(allowedToolsInclude(['search_knowledge'], 'knowledge_search'), true)
  assert.equal(allowedToolsInclude(['grep_chunks'], 'search_knowledge'), true)
  assert.equal(allowedToolsInclude(['wiki_search', 'wiki_read_page'], 'search_knowledge'), false)
  assert.equal(allowedToolsInclude([], 'search_knowledge'), false)
  assert.equal(allowedToolsInclude(undefined, 'search_knowledge'), false)
})
