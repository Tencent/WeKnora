import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test, { type TestContext } from 'node:test'
import * as vue from 'vue'
import { fixture, flush } from '../../test-utils/vueFixture'

type Reply = { success: boolean; data?: unknown }
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: Error) => void
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail })
  return { promise, resolve, reject }
}
const source = readFileSync(new URL('./useSearch.ts', import.meta.url), 'utf8')
const emptyKnowledge = { success: true, data: [] }
const emptyMessages = { success: true, data: { items: [], total: 0 } }
const knowledgeHit = { success: true, data: [{ id: 'chunk', knowledge_id: 'document', knowledge_base_id: 'kb', knowledge_title: 'Handbook', content: 'Passage' }] }
const messageHit = { success: true, data: { items: [{ request_id: 'request', session_id: 'session', session_title: 'Conversation', query_content: 'Question', answer_content: 'Answer' }], total: 1 } }

async function setup(t: TestContext, options: { noKbs?: boolean; discoveryFails?: boolean; scoped?: string[] } = {}) {
  const f = fixture(t)
  const scope = vue.ref(options.scoped ?? [])
  let discoveryFails = options.discoveryFails ?? false
  let discoveries = 0
  const knowledge: Array<ReturnType<typeof deferred<Reply>> & { query: string }> = []
  const messages: Array<ReturnType<typeof deferred<Reply>> & { query: string }> = []
  t.mock.method(console, 'error', () => {})
  const mod = f.evaluate(source, {
    vue,
    '@/api/knowledge-base': { knowledgeSemanticSearch(params: { query: string }) {
      const request = { ...deferred<Reply>(), query: params.query }
      knowledge.push(request)
      return request.promise
    } },
    '@/api/chat-history': { searchMessages(params: { query: string }) {
      const request = { ...deferred<Reply>(), query: params.query }
      messages.push(request)
      return request.promise
    } },
    '@/stores/chatResources': { useChatResourcesStore: () => ({
      async ensureKnowledgeBases() { discoveries++; if (discoveryFails) throw new Error('Unavailable') },
      rawKnowledgeBases: options.noKbs ? [] : [{ id: 'kb', name: 'Library' }],
    }) },
    '@/stores/organization': { useOrganizationStore: () => ({ sharedKnowledgeBases: [], sharedAgents: [] }) },
    '@/stores/menu': { useMenuStore: () => ({ menuArr: [] }) },
  })
  const mounted = f.mount({ setup: () => mod.useCmdkSearch({ agentsEnabled: () => false, lockedKbIds: () => scope.value }) })
  await flush()
  const start = async (query = 'question') => {
    mounted.vm.query = query
    await flush()
    t.mock.timers.tick(350)
    await flush()
  }
  const failures = () => Array.from(mounted.vm.searchFailures ?? [])
  return { ...mounted, scope, knowledge, messages, start, failures,
    discoveries: () => discoveries,
    setDiscoveryFailure: (value: boolean) => { discoveryFails = value },
  }
}

test('a failed knowledge search retains successful message hits and exposes a retryable failure', async t => {
  const f = await setup(t)
  await f.start()
  f.knowledge[0].reject(new DOMException('Timed out', 'TimeoutError'))
  f.messages[0].resolve(messageHit)
  await flush()
  assert.deepEqual(f.failures(), ['knowledge'])
  assert.equal(f.vm.messageGroups[0].sessionId, 'session')
  assert.equal(f.vm.totalMessages, 1)
  assert.equal(f.vm.loading, false)
  f.vm.retrySearch()
  await flush()
  assert.equal(f.vm.loading, true)
  assert.equal(f.vm.messageGroups[0].sessionId, 'session', 'keep useful results while retrying')
  f.knowledge[1].resolve(knowledgeHit)
  f.messages[1].resolve(messageHit)
  await flush()
  assert.deepEqual(f.failures(), [])
  assert.equal(f.vm.fileGroups[0].title, 'Handbook')
  assert.equal(f.vm.loading, false)
})

test('message failure retains successful knowledge hits and both failures remain distinguishable', async t => {
  const f = await setup(t)
  await f.start()
  f.knowledge[0].resolve(knowledgeHit)
  f.messages[0].reject(new Error('Network unavailable'))
  await flush()
  assert.deepEqual(f.failures(), ['messages'])
  assert.equal(f.vm.fileGroups[0].title, 'Handbook')
  assert.equal(f.vm.totalChunks, 1)
  f.vm.retrySearch()
  await flush()
  f.knowledge[1].reject(new Error('Unavailable'))
  f.messages[1].reject(new Error('Unavailable'))
  await flush()
  assert.deepEqual(f.failures(), ['knowledge', 'messages'])
  assert.equal(f.vm.fileGroups.length, 0)
  assert.equal(f.vm.messageGroups.length, 0)
  assert.equal(f.vm.loading, false)
})

for (const knowledge of [[], null]) {
  for (const items of [[], null]) {
    test(`successful empty slices (${knowledge === null ? 'null' : 'array'}/${items === null ? 'null' : 'array'}) are not failures`, async t => {
      const f = await setup(t)
      await f.start()
      f.knowledge[0].resolve({ success: true, data: knowledge })
      f.messages[0].resolve({ success: true, data: { items, total: 0 } })
      await flush()
      assert.deepEqual(f.failures(), [])
      assert.equal(f.vm.hasSearched, true)
      assert.equal(f.vm.loading, false)
      assert.equal(f.vm.fileGroups.length + f.vm.messageGroups.length, 0)
    })
  }
}

for (const reply of [{ success: false }, { success: true }, { success: true, data: {} }]) {
  test(`unsuccessful or malformed knowledge response ${JSON.stringify(reply)} is a failure`, async t => {
    const f = await setup(t)
    await f.start()
    f.knowledge[0].resolve(reply)
    f.messages[0].resolve(emptyMessages)
    await flush()
    assert.deepEqual(f.failures(), ['knowledge'])
    assert.equal(f.vm.loading, false)
  })
}

for (const reply of [{ success: false }, { success: true, data: {} }, { success: true, data: { items: {} } }]) {
  test(`unsuccessful or malformed message response ${JSON.stringify(reply)} is a failure`, async t => {
    const f = await setup(t)
    await f.start()
    f.knowledge[0].resolve(emptyKnowledge)
    f.messages[0].resolve(reply)
    await flush()
    assert.deepEqual(f.failures(), ['messages'])
    assert.equal(f.vm.loading, false)
  })
}

test('failed KB discovery is reported and retried instead of being treated as no accessible KBs', async t => {
  const f = await setup(t, { discoveryFails: true })
  await f.start()
  f.messages[0].resolve(messageHit)
  await flush()
  assert.deepEqual(f.failures(), ['knowledge'])
  assert.equal(f.knowledge.length, 0)
  assert.equal(f.vm.messageGroups.length, 1)
  const before = f.discoveries()
  f.setDiscoveryFailure(false)
  f.vm.retrySearch()
  await flush()
  assert.ok(f.discoveries() > before)
  f.knowledge[0].resolve(knowledgeHit)
  f.messages[1].resolve(emptyMessages)
  await flush()
  assert.deepEqual(f.failures(), [])
  assert.equal(f.vm.fileGroups[0].title, 'Handbook')
})

test('no accessible KBs is a successful empty branch', async t => {
  const empty = await setup(t, { noKbs: true })
  await empty.start()
  empty.messages[0].resolve(emptyMessages)
  await flush()
  assert.equal(empty.knowledge.length, 0)
  assert.deepEqual(empty.failures(), [])
  assert.equal(empty.vm.hasSearched, true)
})

test('scoped searches do not attempt message search or depend on KB discovery', async t => {
  const scoped = await setup(t, { scoped: ['kb'], discoveryFails: true })
  await scoped.start()
  scoped.knowledge[0].reject(new Error('Unavailable'))
  await flush()
  assert.equal(scoped.messages.length, 0)
  assert.deepEqual(scoped.failures(), ['knowledge'])
})

test('retry preserves query and scope and ignores duplicate clicks while a retry is pending', async t => {
  const f = await setup(t, { scoped: ['kb'] })
  await f.start('  question  ')
  f.knowledge[0].reject(new Error('Unavailable'))
  await flush()
  f.vm.retrySearch()
  f.vm.retrySearch()
  await flush()
  assert.equal(f.knowledge.length, 2)
  assert.equal(f.knowledge[1].query, 'question')
  assert.equal(f.vm.query, '  question  ')
  assert.deepEqual(f.scope.value, ['kb'])
  f.knowledge[1].resolve(emptyKnowledge)
  await flush()
  assert.deepEqual(f.failures(), [])
  f.vm.query = ''
  await flush()
  f.vm.retrySearch()
  await flush()
  assert.equal(f.knowledge.length, 2)
})

for (const change of ['query', 'scope', 'clear', 'unmount'] as const) {
  test(`${change} invalidates a late failure from the previous search`, async t => {
    const f = await setup(t)
    await f.start('old query')
    if (change === 'query') f.vm.query = 'new query'
    if (change === 'scope') f.scope.value = ['kb']
    if (change === 'clear') { f.vm.query = ''; f.vm.clearResults() }
    if (change === 'unmount') f.close()
    await flush()
    f.knowledge[0].reject(new Error('Old failure'))
    f.messages[0].reject(new Error('Old failure'))
    await flush()
    assert.deepEqual(f.failures(), [])
    if (change !== 'unmount') assert.equal(f.vm.hasSearched, false)
  })
}
