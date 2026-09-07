import assert from 'node:assert/strict'
import { test } from 'node:test'

import { resolveConfig } from '../dist/config.js'
import { createTools } from '../dist/tools.js'

const signal = new AbortController().signal
const context = { abort: signal }

/** A WeknoraClient stand-in recording calls and answering from fixtures. */
function mockClient(overrides = {}) {
  const calls = []
  return {
    calls,
    async listKnowledgeBases() {
      calls.push(['listKnowledgeBases'])
      return overrides.bases ?? [{ id: 'kb-1', name: 'Manual', description: 'the manual' }]
    },
    async search(request) {
      calls.push(['search', request])
      return overrides.hits ?? [{
        id: 'c-1', knowledge_id: 'k-1', knowledge_title: 'Handbook', chunk_index: 0, score: 0.87,
        content: 'x'.repeat(50),
      }]
    },
    async findDocuments(request) {
      calls.push(['findDocuments', request])
      return overrides.named ?? [{ id: 'k-9', title: 'Handbook annex', knowledge_base_id: 'kb-1', knowledge_base_name: 'Manual' }]
    },
    async getDocument(knowledgeId) {
      calls.push(['getDocument', knowledgeId])
      return overrides.document ?? { title: 'Handbook', description: 'everything', file_name: 'hb.md' }
    },
    async listChunks(request) {
      calls.push(['listChunks', request])
      return overrides.chunks ?? {
        page: 1, pageSize: 20, total: 2,
        chunks: [
          { id: 'c-1', chunk_index: 1, content: 'first passage' },
          { id: 'c-2', chunk_index: 0, content: 'zeroth passage' },
        ],
      }
    },
    async createSession(title) {
      calls.push(['createSession', title])
      return 'sess-1'
    },
    async ask(request) {
      calls.push(['ask', request])
      return overrides.answer ?? {
        answer: ' forty-two ', sessionId: request.sessionId, toolCalls: ['retrieval'],
        references: [{ knowledge_id: 'k-1', knowledge_title: 'Handbook', chunk_index: 3, content: 'the answer' }],
      }
    },
  }
}

test('registers the four prefixed tools and honors toggles', () => {
  const tools = createTools(mockClient(), resolveConfig({ toolPrefix: 'team_kb' }))
  assert.deepEqual(Object.keys(tools).sort(),
    ['team_kb_ask', 'team_kb_list_knowledge_bases', 'team_kb_read_document', 'team_kb_search'])

  const fewer = createTools(mockClient(), resolveConfig({ tools: { ask: false, listKnowledgeBases: false } }))
  assert.deepEqual(Object.keys(fewer).sort(), ['weknora_read_document', 'weknora_search'])
})

test('search returns passages plus named documents, clipped, in the result envelope', async () => {
  const client = mockClient()
  const tools = createTools(client, resolveConfig({ maxChunkChars: 20 }))
  const result = await tools.weknora_search.execute({ query: ' how do I X ' }, context)
  assert.equal(result.metadata.count, 1)
  assert.equal(result.metadata.results[0].document, 'Handbook')
  assert.ok(result.metadata.results[0].content.length <= 21, 'clipped to the budget plus the ellipsis')
  assert.ok(result.metadata.results[0].content.endsWith('…'))
  assert.equal(result.metadata.results[0].truncated, true)
  assert.ok(result.output.includes('score'))
  assert.ok(result.output.includes('Handbook annex'), 'named documents are reported')
  const searchCall = client.calls.find((call) => call[0] === 'search')
  assert.equal(searchCall[1].query, 'how do I X')
})

test('search falls back to every visible knowledge base when unscoped', async () => {
  const client = mockClient()
  const tools = createTools(client, resolveConfig({}))
  await tools.weknora_search.execute({ query: 'q' }, context)
  assert.equal(client.calls[0][0], 'listKnowledgeBases')
  assert.deepEqual(client.calls.find((call) => call[0] === 'search')[1].knowledgeBaseIds, ['kb-1'])
  // The resolved set is cached per process: a second call does not re-list.
  await tools.weknora_search.execute({ query: 'q2' }, context)
  assert.equal(client.calls.filter((call) => call[0] === 'listKnowledgeBases').length, 1)
})

test('configured scope wins over the visible-set fallback', async () => {
  const client = mockClient()
  const tools = createTools(client, resolveConfig({ knowledgeBaseIds: ['kb-mine'] }))
  await tools.weknora_search.execute({ query: 'q' }, context)
  assert.deepEqual(client.calls.find((call) => call[0] === 'search')[1].knowledgeBaseIds, ['kb-mine'])
  assert.equal(client.calls.filter((call) => call[0] === 'listKnowledgeBases').length, 0)
})

test('read_document orders passages and clips, with page-1 summary', async () => {
  const client = mockClient()
  const tools = createTools(client, resolveConfig({}))
  const result = await tools.weknora_read_document.execute({ knowledge_id: 'k-1' }, context)
  assert.equal(result.metadata.title, 'Handbook')
  assert.ok(result.output.indexOf('zeroth passage') < result.output.indexOf('first passage'))
  assert.ok(result.output.includes('Summary: everything'))
  const chunkCall = client.calls.find((call) => call[0] === 'listChunks')
  assert.deepEqual([chunkCall[1].page, chunkCall[1].pageSize], [1, 20])
})

test('ask uses the RAG pipeline, clips references and reports the session', async () => {
  const client = mockClient()
  const tools = createTools(client, resolveConfig({ knowledgeBaseIds: ['kb-1'] }))
  const result = await tools.weknora_ask.execute({ query: 'meaning?' }, context)
  assert.equal(result.metadata.answer, 'forty-two')
  assert.equal(result.metadata.pipeline, 'rag')
  assert.equal(result.metadata.session_id, 'sess-1')
  assert.deepEqual(result.metadata.references[0].document, 'Handbook')
  assert.ok(result.output.includes('Citations:'))
  assert.ok(result.output.includes('session_id'))
  const askCall = client.calls.find((call) => call[0] === 'ask')
  assert.deepEqual(askCall[1].knowledgeBaseIds, ['kb-1'])
})

test('ask with agent_id leaves the scope decision server-side', async () => {
  const client = mockClient()
  const tools = createTools(client, resolveConfig({}))
  await tools.weknora_ask.execute({ query: 'q', agent_id: 'agent-7' }, context)
  const askCall = client.calls.find((call) => call[0] === 'ask')
  assert.equal(askCall[1].agentId, 'agent-7')
  assert.deepEqual(askCall[1].knowledgeBaseIds, [])
  assert.equal(client.calls.filter((call) => call[0] === 'listKnowledgeBases').length, 0)
})
