import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test, { type TestContext } from 'node:test'
import * as vue from 'vue'
import { fixture, flush } from '../../test-utils/vueFixture.ts'

const SEARCH_DELAY_MS = 350
const source = readFileSync(new URL('./useSearch.ts', import.meta.url), 'utf8')

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: Error) => void
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail })
  return { promise, resolve, reject }
}

type Reply = { success: boolean; data?: unknown }
type Request = ReturnType<typeof deferred<Reply>> & {
  query: string
  kbIds?: string[]
}

function chunk(id: string, kbId = 'kb-a', knowledgeId = id) {
  return { id, knowledge_id: knowledgeId, knowledge_base_id: kbId, knowledge_title: id, content: id, chunk_index: 0, score: 0.8 }
}

function message(id: string, sessionId = id) {
  return { request_id: id, session_id: sessionId, session_title: sessionId, query_content: id, score: 0.7 }
}

// 挂载真实 composable，让 Vue 执行监听和卸载；定时器与外部接口由测试控制完成顺序。
async function setup(t: TestContext, options: {
  scoped?: string[]
  pendingKbs?: boolean
  pendingAgents?: boolean
  enabled?: boolean
  debounceMs?: number
  noKbs?: boolean
} = {}) {
  const f = fixture(t)
  const scope = vue.ref(options.scoped ?? [])
  const enabled = vue.ref(options.enabled ?? true)
  const kbs = deferred<void>()
  const agentList = deferred<void>()
  if (!options.pendingKbs) kbs.resolve()
  if (!options.pendingAgents) agentList.resolve()
  const knowledgeRequests: Request[] = []
  const messageRequests: Request[] = []
  const errors = t.mock.method(console, 'error', () => {})
  const resources = {
    ensureKnowledgeBases: t.mock.fn(() => kbs.promise),
    rawKnowledgeBases: options.noKbs ? [] : [{ id: 'kb-a', name: '知识库 A' }, { id: 'kb-b', name: '知识库 B' }],
    ensureAgents: t.mock.fn(() => agentList.promise),
    agents: [{ id: 'agent-a', name: '测试助手', is_builtin: false }],
  }
  const mod = f.evaluate(source, {
    vue,
    '@/api/knowledge-base': {
      knowledgeSemanticSearch: (params: { query: string; knowledge_base_ids: string[] }) => {
        const request = { ...deferred<Reply>(), query: params.query, kbIds: params.knowledge_base_ids }
        knowledgeRequests.push(request)
        return request.promise
      },
    },
    '@/api/chat-history': {
      searchMessages: (params: { query: string }) => {
        const request = { ...deferred<Reply>(), query: params.query }
        messageRequests.push(request)
        return request.promise
      },
    },
    '@/stores/organization': { useOrganizationStore: () => ({ sharedKnowledgeBases: [], sharedAgents: [] }) },
    '@/stores/chatResources': { useChatResourcesStore: () => resources },
    '@/stores/menu': { useMenuStore: () => ({ menuArr: [] }) },
  })
  const mounted = f.mount({
    setup: () => mod.useCmdkSearch({
      lockedKbIds: () => scope.value,
      enabled: () => enabled.value,
      agentsEnabled: () => !!options.pendingAgents,
      ...(options.debounceMs === undefined ? {} : { debounceMs: options.debounceMs }),
    }),
  })
  await flush()
  const input = async (query: string) => { mounted.vm.query = query; await flush() }
  const advance = async (ms = options.debounceMs ?? SEARCH_DELAY_MS) => { t.mock.timers.tick(ms); await flush() }
  const start = async (query: string) => { await input(query); await advance() }
  const complete = async (index: number, id: string) => {
    knowledgeRequests[index]?.resolve({ success: true, data: [chunk(id)] })
    messageRequests[index]?.resolve({ success: true, data: { items: [message(id)], total: 1 } })
    await flush()
  }
  const titles = () => Array.from(mounted.vm.fileGroups, (group: { title: string }) => group.title)
  return { ...mounted, scope, enabled, kbs, agentList, resources, errors, knowledgeRequests, messageRequests, input, advance, start, complete, titles }
}

test('waits for the default debounce and trims the request query', async t => {
  const f = await setup(t)
  await f.input('  alpha  ')
  await f.advance(SEARCH_DELAY_MS - 1)
  assert.equal(f.knowledgeRequests.length, 0)
  await f.advance(1)
  assert.equal(f.knowledgeRequests[0].query, 'alpha')
  assert.equal(f.messageRequests[0].query, 'alpha')
  await f.complete(0, 'alpha')
  assert.deepEqual(f.titles(), ['alpha'])
  assert.equal(f.vm.loading, false)
})

test('continuous input sends only the latest query after its debounce', async t => {
  const f = await setup(t)
  await f.input('a'); await f.advance(200)
  await f.input('ab'); await f.advance(200)
  await f.input('abc'); await f.advance(349)
  assert.equal(f.knowledgeRequests.length, 0)
  await f.advance(1)
  assert.deepEqual(f.knowledgeRequests.map(r => r.query), ['abc'])
  assert.deepEqual(f.messageRequests.map(r => r.query), ['abc'])
  await f.complete(0, 'abc')
})

test('keeps a custom debounce delay', async t => {
  const f = await setup(t, { debounceMs: 50 })
  await f.input('alpha'); await f.advance(49)
  assert.equal(f.knowledgeRequests.length, 0)
  await f.advance(1)
  assert.equal(f.knowledgeRequests.length, 1)
  await f.complete(0, 'alpha')
})

test('clearing input cancels an unsent search', async t => {
  const f = await setup(t)
  await f.input('alpha'); await f.advance(100)
  await f.input('  '); await f.advance()
  assert.equal(f.knowledgeRequests.length, 0)
  assert.equal(f.messageRequests.length, 0)
  assert.equal(f.vm.loading, false)
  assert.equal(f.vm.hasSearched, false)
})

test('changing input immediately clears completed results and totals', async t => {
  const f = await setup(t)
  await f.start('alpha'); await f.complete(0, 'alpha')
  f.vm.query = 'beta'
  assert.deepEqual(f.titles(), [])
  assert.equal(f.vm.messageGroups.length, 0)
  assert.equal(f.vm.totalChunks, 0)
  assert.equal(f.vm.totalMessages, 0)
  assert.equal(f.vm.hasSearched, false)
})

test('does not publish an old response during the next input debounce', async t => {
  const f = await setup(t)
  await f.start('alpha')
  await f.input('beta')
  await f.complete(0, 'alpha')
  assert.deepEqual(f.titles(), [])
  assert.equal(f.vm.messageGroups.length, 0)
  assert.equal(f.vm.loading, false)
  await f.advance(); await f.complete(1, 'beta')
  assert.deepEqual(f.titles(), ['beta'])
})

test('latest results survive responses arriving out of order', async t => {
  const f = await setup(t)
  await f.start('alpha'); await f.start('beta')
  await f.complete(1, 'beta'); await f.complete(0, 'alpha')
  assert.deepEqual(f.titles(), ['beta'])
  assert.equal(f.vm.messageGroups[0].sessionId, 'beta')
  assert.equal(f.vm.loading, false)
})

test('clearing input invalidates both in-flight search endpoints', async t => {
  const f = await setup(t)
  await f.start('alpha'); await f.input('')
  await f.complete(0, 'alpha')
  assert.deepEqual(f.titles(), [])
  assert.equal(f.vm.messageGroups.length, 0)
  assert.equal(f.vm.hasSearched, false)
  assert.equal(f.vm.loading, false)
})

test('same query after a clear belongs to a new search', async t => {
  const f = await setup(t)
  await f.start('alpha'); await f.input(''); await f.start('alpha')
  await f.complete(0, 'old')
  assert.deepEqual(f.titles(), [])
  assert.equal(f.vm.loading, true)
  await f.complete(1, 'new')
  assert.deepEqual(f.titles(), ['new'])
})

test('same-tick A to B to A input does not revive the first A request', async t => {
  const f = await setup(t)
  await f.start('alpha')
  f.vm.query = 'beta'
  f.vm.query = 'alpha'
  await f.complete(0, 'old')
  assert.deepEqual(f.titles(), [])
  await f.advance(); await f.complete(1, 'new')
  assert.deepEqual(f.titles(), ['new'])
})

test('old endpoint failures cannot log errors or end the new loading state', async t => {
  const f = await setup(t)
  await f.start('alpha'); await f.start('beta')
  f.knowledgeRequests[0].reject(new Error('old knowledge failure'))
  f.messageRequests[0].reject(new Error('old message failure'))
  await flush()
  assert.equal(f.errors.mock.callCount(), 0)
  assert.equal(f.vm.loading, true)
  assert.deepEqual(f.titles(), [])
  await f.complete(1, 'beta')
  assert.deepEqual(f.titles(), ['beta'])
})

test('an old partially finished fan-out cannot finish a new search', async t => {
  const f = await setup(t)
  await f.start('alpha')
  f.knowledgeRequests[0].resolve({ success: true, data: [chunk('alpha')] })
  await flush()
  await f.start('beta')
  f.messageRequests[0].resolve({ success: true, data: { items: [message('alpha')], total: 99 } })
  await flush()
  assert.equal(f.vm.loading, true)
  assert.equal(f.vm.totalMessages, 0)
  assert.deepEqual(f.titles(), [])
  await f.complete(1, 'beta')
})

test('current knowledge failure still permits message results', async t => {
  const f = await setup(t)
  await f.start('alpha')
  f.knowledgeRequests[0].reject(new Error('current failure'))
  f.messageRequests[0].resolve({ success: true, data: { items: [message('alpha')], total: 8 } })
  await flush()
  assert.deepEqual(f.titles(), [])
  assert.equal(f.vm.messageGroups[0].sessionId, 'alpha')
  assert.equal(f.vm.totalMessages, 8)
  assert.equal(f.vm.loading, false)
  assert.equal(f.errors.mock.callCount(), 1)
})

test('current failures settle loading and allow the next query to recover', async t => {
  const f = await setup(t)
  await f.start('alpha')
  f.knowledgeRequests[0].reject(new Error('knowledge failure'))
  f.messageRequests[0].reject(new Error('message failure'))
  await flush()
  assert.equal(f.vm.loading, false)
  assert.deepEqual(f.titles(), [])
  await f.start('beta'); await f.complete(1, 'beta')
  assert.deepEqual(f.titles(), ['beta'])
})

test('knowledge scope changes invalidate results before the new debounce', async t => {
  const f = await setup(t, { scoped: ['kb-a'] })
  await f.start('alpha')
  f.scope.value = ['kb-b']
  await f.complete(0, 'old-kb-a')
  assert.deepEqual(f.titles(), [])
  await f.advance()
  assert.deepEqual(Array.from(f.knowledgeRequests[1].kbIds!), ['kb-b'])
  await f.complete(1, 'new-kb-b')
  assert.deepEqual(f.titles(), ['new-kb-b'])
  assert.equal(f.messageRequests.length, 0)
})

test('same-tick knowledge scope A to B to A cannot revive old results', async t => {
  const f = await setup(t, { scoped: ['kb-a'] })
  await f.start('alpha')
  f.scope.value = ['kb-b']
  f.scope.value = ['kb-a']
  await f.complete(0, 'old')
  assert.deepEqual(f.titles(), [])
  await f.advance(); await f.complete(1, 'new')
  assert.deepEqual(f.titles(), ['new'])
})

test('scope changes do not mutate the IDs of an already sent request', async t => {
  const f = await setup(t, { scoped: ['kb-a'] })
  await f.start('alpha')
  f.scope.value.push('kb-b')
  assert.deepEqual(Array.from(f.knowledgeRequests[0].kbIds!), ['kb-a'])
  await f.complete(0, 'old')
  assert.deepEqual(f.titles(), [])
  await f.advance()
  assert.deepEqual(Array.from(f.knowledgeRequests[1].kbIds!), ['kb-a', 'kb-b'])
  await f.complete(1, 'new')
})

test('reordering an unchanged KB set does not send a duplicate request', async t => {
  const f = await setup(t, { scoped: ['kb-a', 'kb-b'] })
  await f.start('alpha')
  f.scope.value = ['kb-b', 'kb-a']
  await flush(); await f.advance()
  assert.equal(f.knowledgeRequests.length, 1)
  await f.complete(0, 'alpha')
  assert.deepEqual(f.titles(), ['alpha'])
})

test('removing KB scope resumes message search and ignores the scoped response', async t => {
  const f = await setup(t, { scoped: ['kb-a'] })
  await f.start('alpha')
  f.scope.value = []
  await flush(); await f.advance()
  assert.equal(f.messageRequests.length, 1)
  f.knowledgeRequests[1].resolve({ success: true, data: [chunk('global')] })
  f.messageRequests[0].resolve({ success: true, data: { items: [message('global')], total: 1 } })
  await flush()
  f.knowledgeRequests[0].resolve({ success: true, data: [chunk('old-scoped')] })
  await flush()
  assert.deepEqual(f.titles(), ['global'])
  assert.equal(f.vm.messageGroups[0].sessionId, 'global')
})

// 全库搜索会先等待知识库列表；过期查询不能在预加载结束后继续发出两个检索请求。
for (const action of ['input', 'clear', 'unmount'] as const) {
  test(`KB preload completion cannot dispatch a search invalidated by ${action}`, async t => {
    const f = await setup(t, { pendingKbs: true })
    await f.start('alpha')
    if (action === 'input') await f.input('beta')
    if (action === 'clear') await f.input('')
    if (action === 'unmount') f.close()
    f.kbs.resolve(); await flush()
    assert.equal(f.knowledgeRequests.length, 0)
    assert.equal(f.messageRequests.length, 0)
    assert.equal(f.vm.loading, false)
    if (action === 'input') {
      await f.advance()
      assert.deepEqual(f.knowledgeRequests.map(r => r.query), ['beta'])
      await f.complete(0, 'beta')
    }
  })
}

test('scope change while KBs load dispatches only the new scoped search', async t => {
  const f = await setup(t, { pendingKbs: true })
  await f.start('alpha')
  f.scope.value = ['kb-b']; await flush(); await f.advance()
  assert.equal(f.knowledgeRequests.length, 1)
  assert.deepEqual(Array.from(f.knowledgeRequests[0].kbIds!), ['kb-b'])
  f.kbs.resolve(); await flush()
  assert.equal(f.knowledgeRequests.length, 1)
  assert.equal(f.messageRequests.length, 0)
  await f.complete(0, 'kb-b')
})

test('unmount cancels an unsent debounce', async t => {
  const f = await setup(t)
  await f.input('alpha'); f.close(); await f.advance()
  assert.equal(f.knowledgeRequests.length, 0)
  assert.equal(f.messageRequests.length, 0)
})

test('unmount invalidates completed results and ignores outstanding responses', async t => {
  const f = await setup(t)
  await f.start('alpha'); f.close()
  await f.complete(0, 'late')
  assert.deepEqual(f.titles(), [])
  assert.equal(f.vm.messageGroups.length, 0)
  assert.equal(f.vm.loading, false)
  assert.equal(f.vm.hasSearched, false)
})

test('unmounted searches ignore rejected endpoints', async t => {
  const f = await setup(t)
  await f.start('alpha'); f.close()
  f.knowledgeRequests[0].reject(new Error('late knowledge error'))
  f.messageRequests[0].reject(new Error('late message error'))
  await flush()
  assert.equal(f.errors.mock.callCount(), 0)
  assert.equal(f.vm.loading, false)
})

test('KB and agent preloads do not write state after unmount', async t => {
  const f = await setup(t, { pendingKbs: true, pendingAgents: true })
  f.close()
  f.kbs.resolve(); f.agentList.resolve(); await flush()
  assert.equal(f.vm.knowledgeBases.length, 0)
  assert.equal(f.vm.agents.length, 0)
  const kbCalls = f.resources.ensureKnowledgeBases.mock.callCount()
  const agentCalls = f.resources.ensureAgents.mock.callCount()
  await f.vm.ensureKbs(); await f.vm.ensureAgents()
  assert.equal(f.resources.ensureKnowledgeBases.mock.callCount(), kbCalls)
  assert.equal(f.resources.ensureAgents.mock.callCount(), agentCalls)
})

test('disabled palette does not search and opening it starts the current query', async t => {
  const f = await setup(t, { enabled: false })
  await f.start('alpha')
  assert.equal(f.knowledgeRequests.length, 0)
  assert.equal(f.vm.loading, false)
  f.enabled.value = true; await flush(); await f.advance()
  assert.equal(f.knowledgeRequests.length, 1)
  await f.complete(0, 'alpha')
})

test('closing and reopening with the same query invalidates the previous request', async t => {
  const f = await setup(t)
  await f.start('alpha')
  f.enabled.value = false
  f.enabled.value = true
  await f.complete(0, 'old')
  assert.deepEqual(f.titles(), [])
  await f.advance(); await f.complete(1, 'new')
  assert.deepEqual(f.titles(), ['new'])
})

test('closing the palette cancels its pending debounce', async t => {
  const f = await setup(t)
  await f.input('alpha'); f.enabled.value = false; await f.advance()
  assert.equal(f.knowledgeRequests.length, 0)
  assert.equal(f.vm.hasSearched, false)
})

test('preserves file/message grouping, KB names and server message totals', async t => {
  const f = await setup(t)
  await f.start('alpha')
  f.knowledgeRequests[0].resolve({ success: true, data: [chunk('a1', 'kb-a', 'doc-a'), chunk('a2', 'kb-a', 'doc-a'), chunk('b', 'kb-b', 'doc-b')] })
  f.messageRequests[0].resolve({ success: true, data: { items: [message('m1', 'session-a'), message('m2', 'session-a')], total: 12 } })
  await flush()
  assert.equal(f.vm.fileGroups.length, 2)
  assert.equal(f.vm.fileGroups[0].chunks.length, 2)
  assert.equal(f.vm.fileGroups[0].kbName, '知识库 A')
  assert.equal(f.vm.fileGroups[1].kbName, '知识库 B')
  assert.equal(f.vm.totalChunks, 3)
  assert.equal(f.vm.messageGroups.length, 1)
  assert.equal(f.vm.messageGroups[0].items.length, 2)
  assert.equal(f.vm.totalMessages, 12)
})

test('no accessible KBs still permits message search', async t => {
  const f = await setup(t, { noKbs: true })
  await f.start('alpha')
  assert.equal(f.knowledgeRequests.length, 0)
  assert.equal(f.messageRequests.length, 1)
  f.messageRequests[0].resolve({ success: true, data: { items: [message('alpha')], total: 1 } })
  await flush()
  assert.equal(f.vm.messageGroups.length, 1)
  assert.equal(f.vm.loading, false)
})
