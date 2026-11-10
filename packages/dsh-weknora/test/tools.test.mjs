import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { WeknoraClient } from '../dist/client.js'
import { resolveConfig } from '../dist/config.js'
import { apply } from '../dist/index.js'
import { createTools } from '../dist/tools.js'
import { assertLosslessJson, assertSupportedSchema, validate } from './helpers/json-schema.mjs'
import { startMockWeknora, ARCH_HANDLE, ARCH_PUBLIC } from './helpers/mock-weknora.mjs'

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
  assert.match(text, /Nothing in WeKnora matched/)
})

test('search rejects a missing query before touching the network', async () => {
  const { byName } = await toolset()
  await assert.rejects(byName.get('weknora_search').execute({}, exec), /"query" is required/)
})

// WeKnora answers 400 when a retrieval names no knowledge base, document or
// tag. Rather than make the model choose a scope it has no basis to choose,
// an unconfigured deployment searches everything the credential can see.
test('search spans every visible knowledge base when none is configured', async () => {
  const { byName, mock } = await toolset()
  const { value } = await call(byName.get('weknora_search'), { query: '默认的检索阈值是多少' })
  assert.deepEqual(value.knowledge_base_ids, ['kb-product', 'kb-ops'])
  const retrieval = mock.requests.find(request => request.path === '/api/v1/knowledge-search')
  assert.deepEqual(retrieval.body.knowledge_base_ids, ['kb-product', 'kb-ops'])
  assert.match(byName.get('weknora_search').description, /every knowledge base this credential can see/)
})

// A deployment can hold dozens of knowledge bases; spelling every id out on
// every search would cost more context than the passages themselves.
test('a wide scope is named by count rather than spelled out', async () => {
  const { byName } = await toolset({ knowledgeBaseIds: ['kb-a', 'kb-b', 'kb-c', 'kb-d'] })
  const { value, text } = await call(byName.get('weknora_search'), { query: '默认的检索阈值是多少' })
  assert.match(text, /searched: 4 knowledge bases/)
  assert.doesNotMatch(text, /kb-a/)
  assert.deepEqual(value.knowledge_base_ids, ['kb-a', 'kb-b', 'kb-c', 'kb-d'], 'the ids stay in the canonical value')
})

test('the resolved full scope is fetched once and reused', async () => {
  const { byName, mock } = await toolset()
  await call(byName.get('weknora_search'), { query: '混合检索 向量' })
  await call(byName.get('weknora_search'), { query: '部署 方式' })
  const listings = mock.requests.filter(request => request.path === '/api/v1/knowledge-bases')
  assert.equal(listings.length, 1, 'resolving the scope must not re-list on every search')
})

test('knowledge_ids alone is a scope WeKnora accepts', async () => {
  const { byName, mock } = await toolset()
  await call(byName.get('weknora_search'), {
    query: '默认的检索阈值是多少',
    knowledge_ids: ['doc-retrieval-pipeline'],
  })
  const retrieval = mock.requests.find(request => request.path === '/api/v1/knowledge-search')
  assert.deepEqual(retrieval.body.knowledge_ids, ['doc-retrieval-pipeline'])
  assert.equal(retrieval.body.knowledge_base_ids, undefined, 'a document-scoped call must not widen to every base')
})

test('a configured default scope is used verbatim', async () => {
  const { byName, mock } = await toolset({ knowledgeBaseIds: ['kb-product'] })
  await call(byName.get('weknora_search'), { query: '默认的检索阈值是多少' })
  assert.equal(mock.requests.some(request => request.path === '/api/v1/knowledge-bases'), false)
  assert.match(byName.get('weknora_search').description, /Searches knowledge base\(s\) kb-product/)
})

// A document nobody quotes is still the document the user asked for by name,
// which is exactly what passage retrieval on its own cannot find.
test('a query naming a document finds it even with no passage match', async () => {
  const { byName } = await toolset({ knowledgeBaseIds: ['kb-ops'] })
  const { value, text } = await call(byName.get('weknora_search'), { query: '灰度发布检查单' })
  assert.equal(value.count, 0)
  assert.deepEqual(value.documents.map(document => document.knowledge_id), ['doc-release-checklist'])
  assert.match(text, /No passage matched "灰度发布检查单", but its name matches document/)
  assert.match(text, /knowledge_id: doc-release-checklist · in Ops runbooks/)
})

test('by-name matches stay inside the scope the call asked for', async () => {
  const { byName } = await toolset({ knowledgeBaseIds: ['kb-product'] })
  const { value } = await call(byName.get('weknora_search'), { query: '灰度发布检查单' })
  assert.deepEqual(value.documents, [], 'a document in kb-ops must not surface in a kb-product search')
})

test('a passage hit is not repeated in the by-name section', async () => {
  const { byName } = await toolset({ knowledgeBaseIds: ['kb-product', 'kb-ops'] })
  const { value } = await call(byName.get('weknora_search'), { query: 'WeKnora 检索流程' })
  const passages = new Set(value.results.map(hit => hit.knowledge_id))
  assert.equal(value.documents.some(document => passages.has(document.knowledge_id)), false)
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

// A long document costs many pages to identify from its passages alone, so
// page 1 carries the title and WeKnora's generated summary.
test('read_document leads with the document title and summary', async () => {
  const { byName } = await toolset()
  const { value, text } = await call(byName.get('weknora_read_document'), {
    knowledge_id: 'doc-retrieval-pipeline',
    page: 1,
    page_size: 2,
  })
  assert.equal(value.title, 'WeKnora 检索流程.md')
  assert.match(value.summary, /混合检索/)
  assert.match(text, /Document WeKnora 检索流程\.md \(doc-retrieval-pipeline\)/)
  assert.match(text, /Summary: /)
})

test('read_document still returns text when the metadata call fails', async () => {
  const { byName, mock } = await toolset()
  mock.fail('/api/v1/knowledge/doc-retrieval-pipeline', 500)
  const { value } = await call(byName.get('weknora_read_document'), { knowledge_id: 'doc-retrieval-pipeline' })
  assert.equal(value.title, '')
  assert.equal(value.summary, '')
  assert.ok(value.content.length > 0, 'losing the metadata must not cost the model the document')
})

test('read_document leads with the title and summary, and only on page 1', async () => {
  const { byName, mock } = await toolset({ maxChunkChars: 4000 })
  const first = await call(byName.get('weknora_read_document'), {
    knowledge_id: 'doc-retrieval-pipeline',
    page: 1,
    page_size: 2,
  })
  assert.equal(first.value.title, 'WeKnora 检索流程.md')
  assert.match(first.text, /^Document WeKnora 检索流程\.md \(doc-retrieval-pipeline\)/)
  assert.match(first.text, /Summary: 讲解 WeKnora 混合检索/)

  const before = mock.requests.length
  const second = await call(byName.get('weknora_read_document'), {
    knowledge_id: 'doc-retrieval-pipeline',
    page: 2,
    page_size: 2,
  })
  assert.equal(second.value.summary, '')
  assert.doesNotMatch(second.text, /Summary:/)
  assert.equal(
    mock.requests.slice(before).some(request => request.path === '/api/v1/knowledge/doc-retrieval-pipeline'),
    false,
    'a later page must not re-fetch metadata the model already has',
  )
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

// The RAG pipeline retrieves only what the request scopes, so an unconfigured
// deployment must resolve the visible set for ask exactly as it does for search.
// Otherwise the quickstart's optional WEKNORA_KNOWLEDGE_BASE_IDS leaves ask
// answering from nothing while search works.
test('ask resolves its own scope when none is configured', async () => {
  const { byName, mock } = await toolset()
  const { value, text } = await call(byName.get('weknora_ask'), { query: '默认的检索阈值是多少' })
  const chat = mock.requests.find(request => request.path.startsWith('/api/v1/knowledge-chat/'))
  assert.deepEqual(chat.body.knowledge_base_ids, ['kb-product', 'kb-ops'])
  assert.ok(value.references.length > 0, 'an unconfigured ask must still retrieve')
  assert.doesNotMatch(text, /没有检索到/)
})

test('ask reuses the scope search already resolved', async () => {
  const { byName, mock } = await toolset()
  await call(byName.get('weknora_search'), { query: '混合检索 向量' })
  await call(byName.get('weknora_ask'), { query: '默认的检索阈值是多少' })
  const listings = mock.requests.filter(request => request.path === '/api/v1/knowledge-bases')
  assert.equal(listings.length, 1, 'search and ask must share the resolved scope')
})

test('a configured scope reaches ask without a listing call', async () => {
  const { byName, mock } = await toolset({ knowledgeBaseIds: ['kb-product'] })
  await call(byName.get('weknora_ask'), { query: '默认的检索阈值是多少' })
  assert.equal(mock.requests.some(request => request.path === '/api/v1/knowledge-bases'), false)
  const chat = mock.requests.find(request => request.path.startsWith('/api/v1/knowledge-chat/'))
  assert.deepEqual(chat.body.knowledge_base_ids, ['kb-product'])
})

// A custom agent resolves its scope server-side from its KBSelectionMode, and
// ids sent from here would override that as an explicit @mention.
test('an agent-pipeline ask leaves the scope to the server', async () => {
  const { byName, mock } = await toolset({ agentId: 'agent-42' })
  await call(byName.get('weknora_ask'), { query: '部署方式有哪些' })
  assert.equal(mock.requests.some(request => request.path === '/api/v1/knowledge-bases'), false)
  const chat = mock.requests.find(request => request.path.startsWith('/api/v1/agent-chat/'))
  assert.equal(chat.body.knowledge_base_ids, undefined)
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

test('search rewrites cited figures to public URLs in passage content', async () => {
  const { byName } = await toolset({ knowledgeBaseIds: ['kb-product'] })
  const { value, text } = await call(byName.get('weknora_search'), { query: '系统架构图' })
  const hit = value.results.find(result => result.knowledge_id === 'doc-architecture')
  assert.ok(hit, 'the architecture document must be in the ranked hits')
  assert.ok(hit.content.includes(`![系统架构](${ARCH_PUBLIC})`))
  assert.ok(text.includes(`![系统架构](${ARCH_PUBLIC})`))
})

test('ask copies public figure URLs from the retrieved passages', async () => {
  const { byName } = await toolset({ knowledgeBaseIds: ['kb-product'] })
  const { value, text } = await call(byName.get('weknora_ask'), { query: '系统架构图长什么样' })
  assert.ok(value.answer.includes(`![系统架构](${ARCH_PUBLIC})`))
  assert.ok(text.includes(`![系统架构](${ARCH_PUBLIC})`))
})

test('handle mode keeps internal resource handles instead of public URLs', async () => {
  const { byName } = await toolset({ knowledgeBaseIds: ['kb-product'], resourceUrls: 'handle' })
  const { value } = await call(byName.get('weknora_search'), { query: '系统架构图' })
  const hit = value.results.find(result => result.knowledge_id === 'doc-architecture')
  assert.ok(hit.content.includes(`![系统架构](${ARCH_HANDLE})`))
})
