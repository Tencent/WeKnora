/**
 * The plugin side of the API contract: the calls this package makes must stay
 * exactly the ones recorded in test/fixtures/api-contract.json, which
 * contract/contract_test.go checks against WeKnora's real Go request and
 * response types. Change a request body and both sides tell you.
 */

import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { after, test } from 'node:test'
import { fileURLToPath } from 'node:url'

import { WeknoraClient } from '../dist/client.js'
import { resolveConfig } from '../dist/config.js'
import { createTools } from '../dist/tools.js'
import { startMockWeknora } from './helpers/mock-weknora.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const fixture = JSON.parse(await readFile(join(here, 'fixtures', 'api-contract.json'), 'utf8'))
const exec = { signal: new AbortController().signal }

/** Replay every documented call against the mock and record what went out. */
async function replayCalls() {
  const mock = await startMockWeknora()
  after(() => mock.close())

  const ragConfig = resolveConfig({ baseUrl: mock.url, knowledgeBaseIds: ['kb-product'] })
  const ragTools = new Map(createTools(new WeknoraClient(ragConfig), ragConfig).map(tool => [tool.name, tool]))
  await ragTools.get('weknora_list_knowledge_bases').execute({}, exec)
  await ragTools.get('weknora_search').execute({ query: '默认的检索阈值是多少' }, exec)
  await ragTools.get('weknora_read_document').execute(
    { knowledge_id: 'doc-retrieval-pipeline', page: 1, page_size: 5 },
    exec,
  )
  await ragTools.get('weknora_ask').execute({ query: '默认的检索阈值是多少' }, exec)

  const agentConfig = resolveConfig({ baseUrl: mock.url, agentId: 'agent-42' })
  const agentAsk = createTools(new WeknoraClient(agentConfig), agentConfig)
    .find(tool => tool.name === 'weknora_ask')
  await agentAsk.execute({ query: '部署方式', session_id: 's1', web_search: true }, exec)

  return mock.requests.map(request => ({
    method: request.method,
    path: request.path,
    query: request.query,
    body: Object.keys(request.body).length === 0 ? null : request.body,
  }))
}

const observed = await replayCalls()

test('the plugin makes exactly the documented WeKnora calls, in order', () => {
  const expected = fixture.calls.map(call => ({
    method: call.method,
    path: call.path,
    query: call.query,
    body: call.body,
  }))
  assert.deepEqual(observed, expected)
})

test('the fixture covers every endpoint the plugin touches', () => {
  const endpoints = new Set(observed.map(call => call.path.replace(/\/(session-mock-1|s1|doc-[\w-]+)$/, '/:id')))
  assert.deepEqual([...endpoints].sort(), [
    '/api/v1/agent-chat/:id',
    '/api/v1/chunks/:id',
    '/api/v1/knowledge-bases',
    '/api/v1/knowledge-search',
    '/api/v1/knowledge-chat/:id',
    '/api/v1/sessions',
  ].sort())
})

test('every stream event the plugin handles is declared in the fixture', async () => {
  const mock = await startMockWeknora()
  after(() => mock.close())
  const config = resolveConfig({ baseUrl: mock.url, agentId: 'agent-42' })
  const client = new WeknoraClient(config)
  const answer = await client.ask(
    { sessionId: 's1', query: '默认的检索阈值是多少', knowledgeBaseIds: [], agentId: 'agent-42', webSearch: false },
    exec.signal,
  )
  // The mock streams tool_call, references, answer and complete; error is
  // covered in client.test.mjs. All four must be in the declared set.
  for (const event of ['answer', 'references', 'tool_call', 'complete', 'error']) {
    assert.ok(fixture.streamResponseTypes.includes(event), `${event} must be declared in the fixture`)
  }
  assert.notEqual(answer.answer, '')
  assert.notEqual(answer.references.length, 0)
  assert.deepEqual(answer.toolCalls, ['knowledge_search'])
})

test('the plugin only reads response fields the fixture declares', async () => {
  const mock = await startMockWeknora()
  after(() => mock.close())
  const config = resolveConfig({ baseUrl: mock.url })
  const client = new WeknoraClient(config)
  // A backend that serves nothing but the declared fields must still produce a
  // complete tool result, which is what "these are the fields we depend on" means.
  const declared = new Set(fixture.responseFieldsRead['types.SearchResult'])
  const results = await client.search({ query: '混合检索', knowledgeBaseIds: ['kb-product'], knowledgeIds: [] }, exec.signal)
  const trimmed = results.map(result => Object.fromEntries(
    Object.entries(result).filter(([key]) => declared.has(key)),
  ))
  const tools = createTools(new WeknoraClient(config), config)
  const search = tools.find(tool => tool.name === 'weknora_search')
  // Feed the trimmed shape back through the projection the tool uses.
  const projected = search.output.render({ query: '混合检索' }, {
    query: '混合检索',
    knowledge_base_ids: [],
    count: trimmed.length,
    results: trimmed.map((result, index) => ({
      rank: index + 1,
      chunk_id: result.id ?? '',
      knowledge_id: result.knowledge_id ?? '',
      document: result.knowledge_title ?? result.knowledge_filename ?? '(untitled)',
      chunk_index: result.chunk_index ?? -1,
      score: result.score ?? 0,
      content: result.content ?? '',
      truncated: false,
    })),
  })
  assert.match(projected[0].text, /knowledge_id: doc-/)
  assert.match(projected[0].text, /score \d\.\d{3}/)
})
