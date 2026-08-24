import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { WeknoraApiError, WeknoraClient } from '../dist/client.js'
import { resolveConfig } from '../dist/config.js'
import { startMockWeknora } from './helpers/mock-weknora.mjs'

const never = new AbortController().signal

/** Start a mock backend and a client wired to it, closed after the test file. */
async function harness(overrides = {}, mockOptions = {}) {
  const mock = await startMockWeknora(mockOptions)
  after(() => mock.close())
  const config = resolveConfig({ baseUrl: mock.url, ...overrides })
  return { mock, client: new WeknoraClient(config), config }
}

test('lists knowledge bases and sends the API key header', async () => {
  const { mock, client } = await harness({ apiKey: 'secret-key' }, { apiKey: 'secret-key' })
  const bases = await client.listKnowledgeBases(never)
  assert.deepEqual(bases.map(base => base.id), ['kb-product', 'kb-ops'])
  assert.equal(mock.requests.at(-1).headers['x-api-key'], 'secret-key')
})

test('a rejected credential surfaces the status and the server reason', async () => {
  const { client } = await harness({ apiKey: 'wrong-key' }, { apiKey: 'right-key' })
  await assert.rejects(client.listKnowledgeBases(never), error => {
    assert.ok(error instanceof WeknoraApiError)
    assert.equal(error.status, 401)
    assert.match(error.message, /HTTP 401: invalid api key/)
    return true
  })
})

test('search posts the documented body and returns ranked hits', async () => {
  const { mock, client } = await harness()
  const results = await client.search(
    { query: '混合检索 的 阈值 是多少', knowledgeBaseIds: ['kb-product'], knowledgeIds: [] },
    never,
  )
  assert.ok(results.length > 0)
  assert.equal(mock.requests.at(-1).path, '/api/v1/knowledge-search')
  assert.deepEqual(mock.requests.at(-1).body.knowledge_base_ids, ['kb-product'])
  assert.ok(results[0].score >= results.at(-1).score)
})

test('resourceUrls=public is forwarded as a query parameter', async () => {
  const { mock, client } = await harness({ resourceUrls: 'public' })
  await client.listKnowledgeBases(never)
  assert.equal(mock.requests.at(-1).query.resource_urls, 'public')
})

test('chunk pages carry their pagination envelope', async () => {
  const { client } = await harness()
  const page = await client.listChunks({ knowledgeId: 'doc-retrieval-pipeline', page: 1, pageSize: 2 }, never)
  assert.equal(page.total, 3)
  assert.equal(page.chunks.length, 2)
  assert.equal(page.pageSize, 2)
})

test('ask creates a session, streams the answer, and collects citations', async () => {
  const { mock, client } = await harness()
  const sessionId = await client.createSession('dsh: 阈值', never)
  const answer = await client.ask(
    { sessionId, query: '默认的检索阈值是多少', knowledgeBaseIds: ['kb-product'], agentId: undefined, webSearch: false },
    never,
  )
  assert.equal(answer.sessionId, 'session-mock-1')
  assert.match(answer.answer, /vector_threshold|向量/)
  assert.ok(answer.references.length > 0)
  assert.deepEqual(answer.toolCalls, [])
  assert.equal(mock.requests.at(-1).path, `/api/v1/knowledge-chat/${sessionId}`)
  assert.equal(mock.requests.at(-1).body.channel, 'api')
})

test('an agent id routes to agent-chat and reports the server-side tools', async () => {
  const { mock, client } = await harness()
  const answer = await client.ask(
    { sessionId: 's1', query: '部署方式有哪些', knowledgeBaseIds: [], agentId: 'agent-9', webSearch: true },
    never,
  )
  assert.equal(mock.requests.at(-1).path, '/api/v1/agent-chat/s1')
  assert.equal(mock.requests.at(-1).body.agent_id, 'agent-9')
  assert.equal(mock.requests.at(-1).body.agent_enabled, true)
  assert.equal(mock.requests.at(-1).body.web_search_enabled, true)
  assert.deepEqual(answer.toolCalls, ['knowledge_search'])
})

test('a streamed error event becomes a failed call, not an empty answer', async () => {
  const { client } = await harness({}, { streamError: true })
  await assert.rejects(
    client.ask({ sessionId: 's1', query: 'x', knowledgeBaseIds: [], agentId: undefined, webSearch: false }, never),
    /streamed an error: model provider unavailable/,
  )
})

// A stream that stops before `complete` was cut short. Returning what arrived
// would present a truncated answer to the model as a whole one.
test('a stream that ends before completing becomes a failed call', async () => {
  const { client } = await harness({}, { streamTruncated: true })
  await assert.rejects(
    client.ask(
      { sessionId: 's1', query: '默认的检索阈值是多少', knowledgeBaseIds: ['kb-product'], agentId: undefined, webSearch: false },
      never,
    ),
    /ended before WeKnora completed the answer/,
  )
})

test('caller cancellation aborts an in-flight call', async () => {
  const { client } = await harness()
  const controller = new AbortController()
  controller.abort()
  await assert.rejects(client.listKnowledgeBases(controller.signal), /the call was cancelled/)
})

test('an unreachable backend reports a transport failure', async () => {
  const client = new WeknoraClient(resolveConfig({ baseUrl: 'http://127.0.0.1:1/api/v1', requestTimeoutMs: 1000 }))
  await assert.rejects(client.listKnowledgeBases(never), WeknoraApiError)
})
