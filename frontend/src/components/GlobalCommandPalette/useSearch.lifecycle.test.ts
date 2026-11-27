import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test, { type TestContext } from 'node:test'
import * as vue from 'vue'
import { fixture, flush } from '../../test-utils/vueFixture'

const SEARCH_DELAY_MS = 350
const source = readFileSync(new URL('./useSearch.ts', import.meta.url), 'utf8')

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: Error) => void
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail })
  return { promise, resolve, reject }
}

type Reply = { success: boolean; data?: unknown }
type Request = ReturnType<typeof deferred<Reply>> & { query: string; kbIds?: string[] }

function chunk(id: string) {
  return { id, knowledge_id: id, knowledge_base_id: 'kb-a', knowledge_title: id, content: id }
}

function message(id: string) {
  return { request_id: id, session_id: id, session_title: id, query_content: id }
}

// Mount the real composable so synchronous watchers and teardown use Vue's scheduler.
// Deferred APIs and the fixture clock expose response ordering without wall-clock delays.
async function setup(t: TestContext, options: {
  scoped?: string[]
  enabled?: boolean
  pendingKbs?: boolean
  pendingAgents?: boolean
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
    rawKnowledgeBases: [{ id: 'kb-a', name: 'Library A' }, { id: 'kb-b', name: 'Library B' }],
    ensureAgents: t.mock.fn(() => agentList.promise),
    agents: [{ id: 'agent-a', name: 'Assistant', is_builtin: false }],
  }
  const mod = f.evaluate(source, {
    vue,
    '@/api/knowledge-base': {
      knowledgeSemanticSearch(params: { query: string; knowledge_base_ids: string[] }) {
        const request = { ...deferred<Reply>(), query: params.query, kbIds: params.knowledge_base_ids }
        knowledgeRequests.push(request)
        return request.promise
      },
    },
    '@/api/chat-history': {
      searchMessages(params: { query: string }) {
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
    }),
  })
  await flush()
  const input = async (query: string) => { mounted.vm.query = query; await flush() }
  const advance = async () => { t.mock.timers.tick(SEARCH_DELAY_MS); await flush() }
  const start = async (query = 'alpha') => { await input(query); await advance() }
  const complete = async (index: number, id: string) => {
    knowledgeRequests[index]?.resolve({ success: true, data: [chunk(id)] })
    messageRequests[index]?.resolve({ success: true, data: { items: [message(id)], total: 7 } })
    await flush()
  }
  const titles = () => Array.from(mounted.vm.fileGroups, (group: { title: string }) => group.title)
  const assertCleared = () => {
    assert.deepEqual(titles(), [])
    assert.equal(mounted.vm.messageGroups.length, 0)
    assert.equal(mounted.vm.totalChunks, 0)
    assert.equal(mounted.vm.totalMessages, 0)
    assert.deepEqual(Array.from(mounted.vm.searchFailures), [])
    assert.equal(mounted.vm.hasSearched, false)
    assert.equal(mounted.vm.loading, false)
  }
  return {
    ...mounted, scope, enabled, kbs, agentList, resources, errors,
    knowledgeRequests, messageRequests, input, advance, start, complete, titles, assertCleared,
  }
}

test('same-tick query A to B to A invalidates the first A request', async t => {
  const f = await setup(t)
  await f.start()
  f.vm.query = 'beta'
  f.vm.query = 'alpha'
  await f.complete(0, 'old')
  f.assertCleared()
  await f.advance()
  assert.deepEqual(f.knowledgeRequests.map(request => request.query), ['alpha', 'alpha'])
  await f.complete(1, 'new')
  assert.deepEqual(f.titles(), ['new'])
})

test('same-tick scope A to B to A invalidates the first scope request', async t => {
  const f = await setup(t, { scoped: ['kb-a'] })
  await f.start()
  f.scope.value = ['kb-b']
  f.scope.value = ['kb-a']
  await f.complete(0, 'old')
  f.assertCleared()
  await f.advance()
  assert.deepEqual(Array.from(f.knowledgeRequests[1].kbIds!), ['kb-a'])
  await f.complete(1, 'new')
  assert.deepEqual(f.titles(), ['new'])
  assert.equal(f.messageRequests.length, 0)
})

for (const change of ['query', 'scope'] as const) {
  test(`${change} changes synchronously clear completed rows and totals`, async t => {
    const f = await setup(t)
    await f.start()
    await f.complete(0, 'old')
    assert.equal(f.vm.totalChunks, 1)
    assert.equal(f.vm.totalMessages, 7)
    if (change === 'query') f.vm.query = 'beta'
    else f.scope.value = ['kb-b']
    f.assertCleared()
  })
}

test('input changes synchronously clear partial failure feedback and surviving results', async t => {
  const f = await setup(t)
  await f.start()
  f.knowledgeRequests[0].reject(new Error('knowledge unavailable'))
  f.messageRequests[0].resolve({ success: true, data: { items: [message('old')], total: 7 } })
  await flush()
  assert.deepEqual(Array.from(f.vm.searchFailures), ['knowledge'])
  assert.equal(f.vm.messageGroups.length, 1)
  f.vm.query = 'beta'
  f.assertCleared()
})

test('input invalidation resets loading and old failures cannot finish the next request', async t => {
  const f = await setup(t)
  await f.start()
  assert.equal(f.vm.loading, true)
  f.vm.query = 'beta'
  f.assertCleared()
  await f.advance()
  assert.equal(f.vm.loading, true)
  f.knowledgeRequests[0].reject(new Error('old knowledge failure'))
  f.messageRequests[0].reject(new Error('old message failure'))
  await flush()
  assert.equal(f.vm.loading, true)
  assert.equal(f.errors.mock.callCount(), 0)
  assert.deepEqual(Array.from(f.vm.searchFailures), [])
  await f.complete(1, 'new')
  assert.deepEqual(f.titles(), ['new'])
})

test('mutating the scope does not change IDs captured by an in-flight request', async t => {
  const f = await setup(t, { scoped: ['kb-a'] })
  await f.start()
  f.scope.value.push('kb-b')
  assert.deepEqual(Array.from(f.knowledgeRequests[0].kbIds!), ['kb-a'])
  await f.complete(0, 'old')
  f.assertCleared()
  await f.advance()
  assert.deepEqual(Array.from(f.knowledgeRequests[1].kbIds!), ['kb-a', 'kb-b'])
  await f.complete(1, 'new')
  assert.deepEqual(f.titles(), ['new'])
})

test('replacing the scope array with reordered IDs preserves the in-flight request', async t => {
  const f = await setup(t, { scoped: ['kb-a', 'kb-b'] })
  await f.start()
  f.scope.value = ['kb-b', 'kb-a']
  await flush()
  await f.advance()
  assert.equal(f.knowledgeRequests.length, 1)
  await f.complete(0, 'current')
  assert.deepEqual(f.titles(), ['current'])
})

test('same-tick close and reopen invalidate the old request without unmounting', async t => {
  const f = await setup(t)
  await f.start()
  f.enabled.value = false
  f.enabled.value = true
  await f.complete(0, 'old')
  f.assertCleared()
  await f.advance()
  await f.complete(1, 'new')
  assert.deepEqual(f.titles(), ['new'])
})

test('a disabled palette waits until opening before searching its current input', async t => {
  const f = await setup(t, { enabled: false })
  await f.start()
  assert.equal(f.knowledgeRequests.length, 0)
  assert.equal(f.messageRequests.length, 0)
  f.assertCleared()
  f.enabled.value = true
  await flush()
  await f.advance()
  assert.deepEqual(f.knowledgeRequests.map(request => request.query), ['alpha'])
  await f.complete(0, 'current')
  assert.deepEqual(f.titles(), ['current'])
})

test('closing cancels an unsent debounce and immediately clears completed results', async t => {
  const f = await setup(t)
  await f.start()
  await f.complete(0, 'old')
  f.enabled.value = false
  f.assertCleared()
  f.enabled.value = true
  await flush()
  f.enabled.value = false
  await f.advance()
  assert.equal(f.knowledgeRequests.length, 1)
  assert.equal(f.messageRequests.length, 1)
})

for (const action of ['close', 'unmount'] as const) {
  test(`KB preload completion cannot dispatch after ${action}`, async t => {
    const f = await setup(t, { pendingKbs: true })
    await f.start()
    assert.equal(f.vm.loading, true)
    if (action === 'close') f.enabled.value = false
    else f.close()
    f.assertCleared()
    f.kbs.resolve()
    await flush()
    assert.equal(f.knowledgeRequests.length, 0)
    assert.equal(f.messageRequests.length, 0)
    f.assertCleared()
  })
}

test('unmount cancels a pending debounce and ignores late endpoint rejections', async t => {
  const f = await setup(t)
  await f.start()
  f.vm.query = 'beta'
  f.close()
  f.knowledgeRequests[0].reject(new Error('late knowledge failure'))
  f.messageRequests[0].reject(new Error('late message failure'))
  await f.advance()
  assert.equal(f.knowledgeRequests.length, 1)
  assert.equal(f.messageRequests.length, 1)
  assert.equal(f.errors.mock.callCount(), 0)
  f.assertCleared()
})

test('KB and agent preload completions do not write state or reload after unmount', async t => {
  const f = await setup(t, { pendingKbs: true, pendingAgents: true })
  f.close()
  f.kbs.resolve()
  f.agentList.resolve()
  await flush()
  assert.equal(f.vm.knowledgeBases.length, 0)
  assert.equal(f.vm.agents.length, 0)
  const kbCalls = f.resources.ensureKnowledgeBases.mock.callCount()
  const agentCalls = f.resources.ensureAgents.mock.callCount()
  await f.vm.ensureKbs()
  await f.vm.ensureAgents()
  assert.equal(f.resources.ensureKnowledgeBases.mock.callCount(), kbCalls)
  assert.equal(f.resources.ensureAgents.mock.callCount(), agentCalls)
})

test('KB and agent preload rejections after unmount do not report obsolete errors', async t => {
  const f = await setup(t, { pendingKbs: true, pendingAgents: true })
  await f.start()
  f.close()
  f.kbs.reject(new Error('late KB failure'))
  f.agentList.reject(new Error('late agent failure'))
  await flush()
  assert.equal(f.errors.mock.callCount(), 0)
  assert.equal(f.knowledgeRequests.length, 0)
  assert.equal(f.messageRequests.length, 0)
  assert.equal(f.vm.knowledgeBases.length, 0)
  assert.equal(f.vm.agents.length, 0)
  f.assertCleared()
})

for (const state of ['closed', 'disposed'] as const) {
  test(`retry cannot dispatch while the palette is ${state}`, async t => {
    const f = await setup(t)
    await f.start()
    await f.complete(0, 'old')
    if (state === 'closed') f.enabled.value = false
    else f.close()
    // The real close handler clears input, but the composable must guard direct retries too.
    f.vm.query = 'alpha'
    f.vm.retrySearch()
    await f.advance()
    assert.equal(f.knowledgeRequests.length, 1)
    assert.equal(f.messageRequests.length, 1)
    f.assertCleared()
  })
}
