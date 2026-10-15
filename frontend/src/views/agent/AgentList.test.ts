import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import * as vue from 'vue'
import * as sorting from '../../utils/resourceSorting.ts'
import { change, fixture, flush } from '../../test-utils/vueFixture.ts'

function setup(t: TestContext) {
  const f = fixture(t)
  const fetchAgentsForList = t.mock.fn(async (_params: unknown, _force: boolean) => ({
    data: [{ id: 'agent', name: 'Updated agent' }], disabled_own_agent_ids: [],
  }))
  const page = f.component(new URL('./AgentList.vue', import.meta.url), {
    'vue-router': { useRoute: () => ({ query: {} }), useRouter: () => ({}) },
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    'tdesign-vue-next': { MessagePlugin: {} },
    '@/stores/auth': { useAuthStore: () => ({ hasRole: () => true }) },
    '@/stores/ui': { useUIStore: () => ({}) },
    '@/stores/organization': { useOrganizationStore: () => ({
      sharedAgents: [], async fetchOrganizations() {}, async fetchSharedAgents() {},
    }) },
    '@/stores/chatResources': { useChatResourcesStore: () => ({ fetchAgentsForList }) },
    '@/stores/settings': { useSettingsStore: () => ({}) },
    '@/stores/menu': { useMenuStore: () => ({}) },
    '@/components/settings/useConfirmDelete': { useConfirmDelete: () => ({}) },
    '@/composables/useTenantModelReadiness': { useTenantModelReadiness: () => ({ loaded: vue.ref(true), isReadyForAgent: vue.ref(true) }) },
    '@/composables/useListUrlState': { useListUrlState: () => ({ scope: vue.ref('all'), creator: vue.ref('all'), query: vue.ref('') }) },
    '@/composables/useResourcePins': { useResourcePins: () => ({ favorites: vue.ref([]), recents: vue.ref([]) }) },
    '@/composables/useAssistantPageContext': { useAssistantPageContext() {} },
    '@/api/agent': {}, '@/api/chat/index': {}, '@/api/organization': {},
    '@/config/contextualGuides': {}, '@/config/settingsRoute': {},
    '@/utils/resourceListSearch': {}, '@/utils/card-list-badge': {}, '@/utils/resourceSorting': sorting,
  })
  return { ...f, ...page, fetchAgentsForList }
}

test('refreshes the mounted agent list once per burst, ignoring other areas and unmounted pages', async t => {
  const f = setup(t)
  await flush()
  assert.equal(f.fetchAgentsForList.mock.callCount(), 1)
  f.fetchAgentsForList.mock.resetCalls()
  f.emit(change('knowledge.update'))
  t.mock.timers.tick(300)
  assert.equal(f.fetchAgentsForList.mock.callCount(), 0)
  f.emit(change('agent.create'))
  f.emit(change('agent.delete'))
  t.mock.timers.tick(300)
  await flush()
  assert.equal(f.fetchAgentsForList.mock.callCount(), 1)
  assert.equal(f.fetchAgentsForList.mock.calls[0].arguments[1], true)
  assert.equal(f.vm.agents[0].name, 'Updated agent')
  f.close()
  f.emit(change('agent.update'))
  t.mock.timers.tick(300)
  assert.equal(f.fetchAgentsForList.mock.callCount(), 1)
})
