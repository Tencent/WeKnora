import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test, { type TestContext } from 'node:test'
import * as vue from 'vue'
import * as pinia from 'pinia'
import * as resources from './resourceCache.ts'
import * as localized from './localizedResourceCache.ts'
import { change, fixture, flush } from '../test-utils/vueFixture.ts'

function setup(t: TestContext) {
  const f = fixture(t)
  const api = {
    listKnowledgeBases: t.mock.fn(async (_params?: { view?: string }) => ({ data: [{ id: 'kb' }] })),
    getKnowledgeBaseById: t.mock.fn(async () => ({ data: { id: 'kb' } })),
    listAgents: t.mock.fn(async () => ({ data: [{ id: 'agent' }] })),
    listModels: t.mock.fn(async () => [{ id: 'model' }]),
  }
  const { useChatResourcesStore } = f.evaluate(readFileSync(new URL('./chatResources.ts', import.meta.url), 'utf8'), {
    vue, pinia, '@/utils/assistantDataChange': f.dataChange,
    '@/api/knowledge-base': api, '@/api/agent': api,
    '@/api/model': api, '@/api/web-search-provider': {}, '@/api/system': {},
    '@/utils/request': { getCurrentLanguage: () => 'en-US' },
    '@/stores/organization': { useOrganizationStore: () => ({
      async fetchSharedKnowledgeBases() {}, async fetchSharedAgents() {},
    }) },
    './resourceCache': resources, './localizedResourceCache': localized,
  })
  const root = pinia.createPinia()
  pinia.setActivePinia(root)
  t.after(() => pinia.disposePinia(root))
  return { ...f, api, store: useChatResourcesStore() }
}

test('refreshes agent selectors and invalidates per-agent KB access snapshots', async t => {
  const { store, api, emit } = setup(t)
  await store.ensureAgentKnowledgeBases('agent')
  await store.ensureAgentKnowledgeBases('agent')
  assert.equal(api.listKnowledgeBases.mock.callCount(), 1)
  emit(change('agent.update', { agent_id: ['agent'] }))
  t.mock.timers.tick(300)
  await flush()
  assert.equal(api.listAgents.mock.callCount(), 1)
  assert.equal(store.agents[0].id, 'agent')
  await store.ensureAgentKnowledgeBases('agent')
  assert.equal(api.listKnowledgeBases.mock.callCount(), 2)
  store.$dispose()
  emit(change('agent.delete'))
  t.mock.timers.tick(300)
  await flush()
  assert.equal(api.listAgents.mock.callCount(), 1)
})

for (const operation of ['knowledge_base.generate_profile', 'knowledge.delete', 'faq.update_entry', 'wiki.revert']) {
  test(`refreshes KB selectors and invalidates details on ${operation}`, async t => {
    const { store, api, emit } = setup(t)
    await store.fetchKnowledgeBaseById('kb')
    await store.fetchKnowledgeBaseById('kb')
    assert.equal(api.getKnowledgeBaseById.mock.callCount(), 1)
    emit(change(operation, { knowledge_base_id: ['kb'] }))
    t.mock.timers.tick(300)
    await flush()
    assert.equal(api.listKnowledgeBases.mock.callCount(), 1)
    await store.fetchKnowledgeBaseById('kb')
    assert.equal(api.getKnowledgeBaseById.mock.callCount(), 2)
  })
}

test('dashboard refresh replaces an in-flight full KB snapshot after an assistant change', async t => {
  const { store, api, emit } = setup(t)
  let fullReads = 0
  let finishStaleRead!: (value: { data: { id: string }[] }) => void
  api.listKnowledgeBases.mock.mockImplementation(async (params) => {
    if (params?.view === 'list') return { data: [{ id: 'card' }] }
    fullReads += 1
    if (fullReads === 1) return { data: [{ id: 'before' }] }
    if (fullReads === 2) {
      return new Promise(resolve => { finishStaleRead = resolve })
    }
    return { data: [{ id: 'current' }] }
  })

  await store.ensureKnowledgeBases()
  emit(change('knowledge_base.delete', { knowledge_base_id: ['before'] }))
  t.mock.timers.tick(300)
  assert.equal(fullReads, 2)

  assert.deepEqual(await store.fetchKnowledgeBasesForList(undefined, true), [{ id: 'card' }])
  finishStaleRead({ data: [{ id: 'stale' }] })
  await flush()

  assert.equal(fullReads, 3)
  assert.deepEqual(store.rawKnowledgeBases, [{ id: 'current' }])
})

test('forcing a cold dashboard refresh only fetches the lightweight list', async t => {
  const { store, api } = setup(t)
  await store.fetchKnowledgeBasesForList(undefined, true)
  await flush()
  assert.equal(api.listKnowledgeBases.mock.callCount(), 1)
  // The store is evaluated in a separate VM context.
  assert.deepEqual({ ...api.listKnowledgeBases.mock.calls[0].arguments[0] }, {
    creator: undefined,
    view: 'list',
  })
  assert.equal(store.isLoaded('knowledgeBases'), false)
})

test('refreshes the model selectors on a model change', async t => {
  const { store, api, emit } = setup(t)
  emit(change('model.update'))
  t.mock.timers.tick(300)
  await flush()
  assert.equal(api.listModels.mock.callCount(), 1)
  assert.equal(store.allModels[0].id, 'model')
  assert.equal(api.listAgents.mock.callCount(), 0)
})

test('ignores unknown areas', async t => {
  const { api, emit } = setup(t)
  emit(change('unknown.update'))
  t.mock.timers.tick(300)
  await flush()
  assert.equal(api.listAgents.mock.callCount(), 0)
  assert.equal(api.listKnowledgeBases.mock.callCount(), 0)
  assert.equal(api.listModels.mock.callCount(), 0)
})
