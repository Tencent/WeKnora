import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import * as vue from 'vue'
import { fixture, flush } from '../../test-utils/vueFixture.ts'
import * as sortPreference from '../../utils/resourceSortPreference.ts'

const plain = <T>(value: T): T => JSON.parse(JSON.stringify(value))

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: Error) => void
  const promise = new Promise<T>((accept, fail) => { resolve = accept; reject = fail })
  return { promise, resolve, reject }
}

function commonDeps(scopeRef: vue.Ref<string>) {
  return {
    'vue-router': {
      useRoute: () => vue.reactive({ query: {} }),
      useRouter: () => ({ push() {}, replace() {} }),
    },
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    'tdesign-vue-next': {
      MessagePlugin: { success() {}, error() {}, warning() {}, info() {} },
      Icon: {},
    },
    '@/stores/auth': { useAuthStore: () => ({ hasRole: () => true, user: { id: 'u-1' }, isLiteMode: false }) },
    '@/stores/ui': { useUIStore: () => ({ showKBEditorModal: false }) },
    '@/stores/settings': { useSettingsStore: () => ({}) },
    '@/stores/menu': { useMenuStore: () => ({}) },
    '@/stores/assistant': { useAssistantStore: () => ({}) },
    '@/composables/useAssistantPageContext': { useAssistantPageContext() {} },
    '@/composables/useTenantModelReadiness': {
      useTenantModelReadiness: () => ({
        loaded: vue.ref(true),
        isReadyForAgent: vue.ref(true),
        isReadyForDocumentKb: vue.ref(true),
      }),
    },
    '@/composables/useListUrlState': {
      useListUrlState: () => ({
        scope: scopeRef,
        creator: vue.ref('all'),
        query: vue.ref(''),
      }),
    },
    '@/composables/useResourcePins': {
      useResourcePins: () => ({
        favorites: vue.ref([]),
        recents: vue.ref([]),
        isFavorite: () => false,
        toggleFavorite() {},
        touchRecent() {},
      }),
    },
    '@/components/settings/useConfirmDelete': {
      useConfirmDelete: () => ({ confirmDelete: async () => false }),
    },
    '@/config/contextualGuides': {
      focusAgentEditorSection() {},
      isContextualGuideDone: () => true,
      markContextualGuideDone() {},
    },
    '@/config/settingsRoute': { integrationSectionKey: 'integration' },
    '@/utils/resourceListSearch': { matchesResourceQuery: () => true },
    '@/utils/card-list-badge': { shouldShowResourceOriginBadge: () => false },
    '@/utils/kbPermission': { permissionCanManageKB: () => true },
    '@/utils/agentMcp': { agentUsesMcp: () => false },
    '@/utils/index': { formatStringDate: (d: Date) => d.toISOString() },
    '@/utils/resourceSortPreference': sortPreference,
    '@/utils/resourceSorting': {
      DEFAULT_RESOURCE_SORT: 'updated_desc',
      sortResourcesWithinGroups: (items: any[]) => items,
    },
  }
}

async function agentSetup(t: TestContext, initialScope = 'mine') {
  const f = fixture(t)
  const scope = vue.ref(initialScope)
  const requests: Array<{ orgId: string; d: ReturnType<typeof deferred<any>> }> = []
  const listOrganizationSharedAgents = t.mock.fn((orgId: string) => {
    const d = deferred<any>()
    requests.push({ orgId, d })
    return d.promise
  })
  const page = f.component(new URL('./AgentList.vue', import.meta.url), {
    ...commonDeps(scope),
    '@/api/agent': { deleteAgent: async () => {}, copyAgent: async () => {} },
    '@/api/chat/index': { createSessions: async () => ({}) },
    '@/api/organization': {
      setSharedAgentDisabledByMe: async () => ({}),
      listOrganizationSharedAgents,
    },
    '@/stores/chatResources': {
      useChatResourcesStore: () => ({
        fetchAgentsForList: async () => ({ data: [], disabled_own_agent_ids: [] }),
      }),
    },
    '@/stores/organization': {
      useOrganizationStore: () => ({
        sharedAgents: [],
        resourceCounts: { agents: { by_organization: {} } },
        fetchOrganizations: async () => {},
        fetchSharedAgents: async () => {},
      }),
    },
  })
  await flush()
  return { vm: page.vm, close: page.close, scope, requests }
}

async function kbListSetup(t: TestContext, initialScope = 'mine') {
  const f = fixture(t)
  const scope = vue.ref(initialScope)
  const requests: Array<{ orgId: string; d: ReturnType<typeof deferred<any>> }> = []
  const listOrganizationSharedKnowledgeBases = t.mock.fn((orgId: string) => {
    const d = deferred<any>()
    requests.push({ orgId, d })
    return d.promise
  })
  const page = f.component(new URL('../knowledge/KnowledgeBaseList.vue', import.meta.url), {
    ...commonDeps(scope),
    '@/api/knowledge-base': {
      deleteKnowledgeBase: async () => {},
      duplicateKnowledgeBase: async () => {},
      togglePinKnowledgeBase: async () => {},
    },
    '@/api/organization': { listOrganizationSharedKnowledgeBases },
    '@/stores/chatResources': {
      useChatResourcesStore: () => ({
        fetchKnowledgeBasesForList: async () => [],
      }),
    },
    '@/stores/organization': {
      useOrganizationStore: () => ({
        sharedKnowledgeBases: [],
        resourceCounts: { knowledge_bases: { by_organization: {} } },
        fetchOrganizations: async () => {},
        fetchSharedKnowledgeBases: async () => {},
      }),
    },
  })
  await flush()
  return { vm: page.vm, close: page.close, scope, requests }
}

test('AgentList ignores out-of-order space responses and preserves loading state', async (t) => {
  const s = await agentSetup(t)
  s.scope.value = 'org-a'
  await flush()
  assert.equal(s.requests.length, 1)
  assert.equal(s.vm.spaceAgentsLoading, true)

  s.scope.value = 'org-b'
  await flush()
  assert.equal(s.requests.length, 2)
  assert.equal(s.vm.spaceAgentsLoading, true)

  // Slow org-a response resolves while org-b is still loading
  s.requests[0].d.resolve({ success: true, data: [{ agent: { id: 'a1', name: 'A' } }] })
  await flush()
  assert.deepEqual(plain(s.vm.spaceAgentsList), [])
  assert.equal(s.vm.spaceAgentCountByOrg['org-a'], undefined)
  assert.equal(s.vm.spaceAgentsLoading, true)

  s.requests[1].d.resolve({ success: true, data: [{ agent: { id: 'b1', name: 'B' } }, { agent: { id: 'b2', name: 'B2' } }] })
  await flush()
  assert.equal(s.vm.spaceAgentsList.length, 2)
  assert.equal(s.vm.spaceAgentsList[0].agent.id, 'b1')
  assert.equal(s.vm.spaceAgentCountByOrg['org-b'], 2)
  assert.equal(s.vm.spaceAgentsLoading, false)
})

test('AgentList resets loading and ignores late responses when leaving a space or failing', async (t) => {
  const s = await agentSetup(t)
  s.scope.value = 'org-a'
  await flush()
  assert.equal(s.vm.spaceAgentsLoading, true)

  s.scope.value = 'mine'
  await flush()
  assert.equal(s.vm.spaceAgentsLoading, false)
  assert.deepEqual(plain(s.vm.spaceAgentsList), [])

  s.requests[0].d.resolve({ success: true, data: [{ agent: { id: 'a1' } }] })
  await flush()
  assert.deepEqual(plain(s.vm.spaceAgentsList), [])

  // Error on current space clears list and loading without unhandled rejection
  s.scope.value = 'org-err'
  await flush()
  assert.equal(s.vm.spaceAgentsLoading, true)
  s.requests[1].d.reject(new Error('network error'))
  await flush()
  assert.equal(s.vm.spaceAgentsLoading, false)
  assert.deepEqual(plain(s.vm.spaceAgentsList), [])
})

test('KnowledgeBaseList ignores out-of-order space responses and preserves loading state', async (t) => {
  const s = await kbListSetup(t)
  s.scope.value = 'org-a'
  await flush()
  assert.equal(s.requests.length, 1)
  assert.equal(s.vm.spaceKbsLoading, true)

  s.scope.value = 'org-b'
  await flush()
  assert.equal(s.requests.length, 2)
  assert.equal(s.vm.spaceKbsLoading, true)

  s.requests[0].d.resolve({ success: true, data: [{ knowledge_base: { id: 'kb-a' } }] })
  await flush()
  assert.deepEqual(plain(s.vm.spaceKbsList), [])
  assert.equal(s.vm.spaceCountByOrg['org-a'], undefined)
  assert.equal(s.vm.spaceKbsLoading, true)

  s.requests[1].d.resolve({ success: true, data: [{ knowledge_base: { id: 'kb-b1' } }, { knowledge_base: { id: 'kb-b2' } }] })
  await flush()
  assert.equal(s.vm.spaceKbsList.length, 2)
  assert.equal(s.vm.spaceKbsList[0].knowledge_base.id, 'kb-b1')
  assert.equal(s.vm.spaceCountByOrg['org-b'], 2)
  assert.equal(s.vm.spaceKbsLoading, false)
})

test('KnowledgeBaseList normalizes legacy shared scope and ignores late or unmounted responses', async (t) => {
  const s = await kbListSetup(t)
  s.scope.value = 'org-a'
  await flush()
  assert.equal(s.vm.spaceKbsLoading, true)

  s.scope.value = 'shared'
  await flush()
  assert.equal(s.scope.value, 'all')
  assert.equal(s.vm.spaceKbsLoading, false)
  assert.deepEqual(plain(s.vm.spaceKbsList), [])

  s.requests[0].d.resolve({ success: true, data: [{ knowledge_base: { id: 'kb-a' } }] })
  await flush()
  assert.deepEqual(plain(s.vm.spaceKbsList), [])

  s.scope.value = 'org-b'
  await flush()
  s.close()
  s.requests[1].d.resolve({ success: true, data: [{ knowledge_base: { id: 'kb-b-late' } }] })
  await flush()
  assert.deepEqual(plain(s.vm.spaceKbsList), [])
  assert.equal(s.vm.spaceCountByOrg['org-b'], undefined)
})

test('AgentList ignores late responses after unmount', async (t) => {
  const s = await agentSetup(t)
  s.scope.value = 'org-a'
  await flush()
  s.close()
  s.requests[0].d.resolve({ success: true, data: [{ agent: { id: 'a-late' } }] })
  await flush()
  assert.deepEqual(plain(s.vm.spaceAgentsList), [])
  assert.equal(s.vm.spaceAgentCountByOrg['org-a'], undefined)
})

test('AgentList clears the previous space list while the next space is loading', async (t) => {
  const a = await agentSetup(t)
  a.scope.value = 'org-a'
  await flush()
  a.requests[0].d.resolve({ success: true, data: [{ agent: { id: 'a1' } }] })
  await flush()
  assert.equal(a.vm.spaceAgentsList.length, 1)
  a.scope.value = 'org-b'
  await flush()
  assert.equal(a.vm.spaceAgentsLoading, true)
  assert.deepEqual(plain(a.vm.spaceAgentsList), [])
})

test('KnowledgeBaseList clears the previous space list while the next space is loading', async (t) => {
  const k = await kbListSetup(t)
  k.scope.value = 'org-a'
  await flush()
  k.requests[0].d.resolve({ success: true, data: [{ knowledge_base: { id: 'kb-a' } }] })
  await flush()
  assert.equal(k.vm.spaceKbsList.length, 1)
  k.scope.value = 'org-b'
  await flush()
  assert.equal(k.vm.spaceKbsLoading, true)
  assert.deepEqual(plain(k.vm.spaceKbsList), [])
})
