import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { WeknoraClient } from '../dist/client.js'
import { resolveConfig } from '../dist/config.js'
import { apply } from '../dist/index.js'
import { createTools } from '../dist/tools.js'
import { assertLosslessJson, assertSupportedSchema, validate } from './helpers/json-schema.mjs'
import { startMockWeknora } from './helpers/mock-weknora.mjs'

const never = new AbortController().signal
const exec = { signal: never }

/** Build the tools against a live mock backend. */
async function toolset(overrides = {}) {
  const mock = await startMockWeknora()
  after(() => mock.close())
  const config = resolveConfig({ baseUrl: mock.url, ...overrides })
  const tools = createTools(new WeknoraClient(config), config)
  return { mock, config, byName: new Map(tools.map(tool => [tool.name, tool])), tools }
}

/** Run a tool the way the registry does: execute, then validate and render. */
async function call(tool, args) {
  const value = await tool.execute(args, exec)
  assertLosslessJson(value, tool.name)
  const violations = validate(tool.output.schema, value)
  assert.deepEqual(violations, [], `${tool.name} returned a value its output schema rejects`)
  const content = tool.output.render(args, value)
  assert.ok(Array.isArray(content) && content.length > 0 && content[0].type === 'text')
  return { value, text: content.map(block => block.text).join('\n') }
}

test('every declared schema stays inside the supported subset', async () => {
  const { tools } = await toolset()
  for (const tool of tools) {
    assertSupportedSchema(tool.parameters, `${tool.name}/parameters`)
    assertSupportedSchema(tool.output.schema, `${tool.name}/output`)
    assert.match(tool.name, /^[a-z][a-z0-9_]*$/)
    assert.ok(tool.description.length > 40, `${tool.name} needs a description the model can act on`)
  }
})

test('the default composition registers exactly the four documented tools', async () => {
  const { tools } = await toolset()
  assert.deepEqual(tools.map(tool => tool.name).sort(), [
    'weknora_ask',
    'weknora_list_knowledge_bases',
    'weknora_read_document',
    'weknora_search',
  ])
})

test('list_knowledge_bases renders ids the model can pass back', async () => {
  const { byName } = await toolset()
  const { value, text } = await call(byName.get('weknora_list_knowledge_bases'), {})
  assert.equal(value.count, 2)
  assert.match(text, /Product docs \(id: kb-product\)/)
})

test('search returns ranked passages carrying their knowledge_id', async () => {
  const { byName } = await toolset({ knowledgeBaseIds: ['kb-product'] })
  const { value, text } = await call(byName.get('weknora_search'), { query: '默认的检索阈值是多少' })
  assert.ok(value.count > 0)
  assert.equal(value.results[0].rank, 1)
  assert.ok(value.results[0].knowledge_id.startsWith('doc-'))
  assert.match(text, /passage\(s\) for "默认的检索阈值是多少"/)
  assert.match(text, /knowledge_id: doc-retrieval-pipeline/)
})

test('search respects max_results and the configured ceiling', async () => {
  const { byName } = await toolset({ maxResults: 2, knowledgeBaseIds: ['kb-product'] })
  const wide = await call(byName.get('weknora_search'), { query: '检索 部署 向量 阈值' })
  assert.ok(wide.value.count <= 2)
  const narrow = await call(byName.get('weknora_search'), { query: '检索 部署 向量 阈值', max_results: 1 })
  assert.equal(narrow.value.count, 1)
})

test('search falls back to the configured knowledge base scope', async () => {
  const { byName, mock } = await toolset({ knowledgeBaseIds: ['kb-ops'] })
  await call(byName.get('weknora_search'), { query: '部署 方式' })
  assert.deepEqual(mock.requests.at(-1).body.knowledge_base_ids, ['kb-ops'])
})

test('an empty result set tells the model what to try next', async () => {
  const { byName } = await toolset({ knowledgeBaseIds: ['kb-product'] })
  const { value, text } = await call(byName.get('weknora_search'), { query: '量子纠缠咖啡机保修期' })
  assert.equal(value.count, 0)
  assert.match(text, /No passage in WeKnora matched/)
})

test('search rejects a missing query before touching the network', async () => {
  const { byName } = await toolset()
  await assert.rejects(byName.get('weknora_search').execute({}, exec), /"query" is required/)
})

// WeKnora answers 400 when a retrieval names no knowledge base, document or
// tag. Without a configured default that is the model's most likely first
// call, so the scope has to be demanded in the description and enforced here.
test('search demands a scope when the deployment configures no default', async () => {
  const { byName, mock } = await toolset()
  const requestsBefore = mock.requests.length
  await assert.rejects(
    byName.get('weknora_search').execute({ query: '默认的检索阈值是多少' }, exec),
    /needs a scope to search/,
  )
  assert.equal(mock.requests.length, requestsBefore, 'the unscoped call must not reach WeKnora')
  assert.match(byName.get('weknora_search').description, /must name knowledge_base_ids/)
  assert.match(byName.get('weknora_search').description, /weknora_list_knowledge_bases/)
})

test('knowledge_ids alone is a scope WeKnora accepts', async () => {
  const { byName, mock } = await toolset()
  await call(byName.get('weknora_search'), {
    query: '默认的检索阈值是多少',
    knowledge_ids: ['doc-retrieval-pipeline'],
  })
  assert.deepEqual(mock.requests.at(-1).body.knowledge_ids, ['doc-retrieval-pipeline'])
})

test('a configured default scope keeps the description free of the demand', async () => {
  const { byName } = await toolset({ knowledgeBaseIds: ['kb-product'] })
  assert.match(byName.get('weknora_search').description, /Defaults to knowledge base\(s\) kb-product/)
  assert.doesNotMatch(byName.get('weknora_search').description, /must name knowledge_base_ids/)
})

test('long passages are clipped and marked truncated', async () => {
  const { byName } = await toolset({ maxChunkChars: 20, knowledgeBaseIds: ['kb-product'] })
  const { value, text } = await call(byName.get('weknora_search'), { query: '混合检索 向量 关键词' })
  assert.ok(value.results[0].truncated)
  assert.ok(value.results[0].content.length <= 21)
  assert.match(text, /passage truncated/)
})

test('read_document reassembles passages in order and reports paging', async () => {
  const { byName } = await toolset({ maxChunkChars: 4000 })
  const first = await call(byName.get('weknora_read_document'), {
    knowledge_id: 'doc-retrieval-pipeline',
    page: 1,
    page_size: 2,
  })
  assert.equal(first.value.total_chunks, 3)
  assert.equal(first.value.returned_chunks, 2)
  assert.equal(first.value.has_more, true)
  assert.match(first.text, /request page 2/)
  const second = await call(byName.get('weknora_read_document'), {
    knowledge_id: 'doc-retrieval-pipeline',
    page: 2,
    page_size: 2,
  })
  assert.equal(second.value.has_more, false)
})

test('read_document surfaces a backend 404 as a failed call', async () => {
  const { byName } = await toolset()
  await assert.rejects(
    byName.get('weknora_read_document').execute({ knowledge_id: 'missing-doc' }, exec),
    /HTTP 404/,
  )
})

test('ask returns the composed answer, citations and a resumable session', async () => {
  const { byName, mock } = await toolset()
  const { value, text } = await call(byName.get('weknora_ask'), { query: '默认的检索阈值是多少' })
  assert.equal(value.pipeline, 'rag')
  assert.equal(value.session_id, 'session-mock-1')
  assert.ok(value.references.length > 0)
  assert.match(text, /Citations:/)
  assert.match(text, /pass session_id to ask a follow-up/)
  assert.equal(mock.requests.some(request => request.path === '/api/v1/sessions'), true)
})

test('ask reuses a given session instead of creating one', async () => {
  const { byName, mock } = await toolset()
  await call(byName.get('weknora_ask'), { query: '部署方式', session_id: 'session-existing' })
  assert.equal(mock.requests.some(request => request.path === '/api/v1/sessions'), false)
  assert.equal(mock.requests.at(-1).path, '/api/v1/knowledge-chat/session-existing')
})

test('a configured agent id switches ask to the agent pipeline', async () => {
  const { byName } = await toolset({ agentId: 'agent-42' })
  const { value, text } = await call(byName.get('weknora_ask'), { query: '部署方式有哪些' })
  assert.equal(value.pipeline, 'agent')
  assert.deepEqual(value.tool_calls, ['knowledge_search'])
  assert.match(text, /WeKnora tools used: knowledge_search/)
})

test('apply registers into ctx.tools and honours the prefix and toggles', () => {
  const registered = []
  const disposers = []
  const ctx = {
    tools: {
      register(definition) {
        registered.push(definition.name)
        const disposer = () => disposers.push(definition.name)
        return disposer
      },
    },
  }
  apply(ctx, { baseUrl: 'https://kb.example.com', toolPrefix: 'kb', tools: { ask: false, readDocument: false } })
  assert.deepEqual(registered.sort(), ['kb_list_knowledge_bases', 'kb_search'])
})

test('apply fails the plugin load on an invalid row', () => {
  const ctx = { tools: { register: () => () => undefined } }
  assert.throws(() => apply(ctx, { baseUrl: 'ftp://kb.example.com' }), /dsh-weknora configuration is invalid/)
})
