import assert from 'node:assert/strict'
import test from 'node:test'
import * as vue from 'vue'
import * as marked from 'marked'
import * as literalTilde from '../../utils/markedLiteralTilde.ts'
import * as sorting from '../../utils/resourceSorting.ts'
import { change, fixture, flush } from '../../test-utils/vueFixture.ts'

test('refreshes data source controls after assistant changes and stops on unmount', async t => {
  const f = fixture(t)
  const listDataSources = t.mock.fn(async (_id: string) => ({ data: [] }))
  const { close } = f.component(new URL('./settings/DataSourceSettings.vue', import.meta.url), {
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    'tdesign-vue-next': { MessagePlugin: { error: assert.fail } },
    '@/stores/auth': { useAuthStore: () => ({ hasRole: () => true }) },
    '@/utils/cronHumanize': {},
    '@/composables/useConnectorLabel': { useConnectorLabel: () => ({ connectorLabel: (type: string) => type }) },
    '@/api/datasource': { listDataSources },
  }, { kbId: 'kb' })
  await flush()
  listDataSources.mock.resetCalls()
  f.emit(change('agent.update'))
  t.mock.timers.tick(300)
  assert.equal(listDataSources.mock.callCount(), 0)
  f.emit(change('data_source.pause'))
  f.emit(change('data_source.sync'))
  f.emit(change('data_source.update_sync'))
  t.mock.timers.tick(300)
  await flush()
  assert.equal(listDataSources.mock.callCount(), 1)
  assert.equal(listDataSources.mock.calls[0].arguments[0], 'kb')
  close()
  f.emit(change('data_source.resume'))
  t.mock.timers.tick(300)
  assert.equal(listDataSources.mock.callCount(), 1)
})

test('refreshes the FAQ entries and tags once per burst for its own KB only', async t => {
  const f = fixture(t)
  const listFAQEntries = t.mock.fn(async (_id: string, _params: unknown) => ({ data: { data: [], total: 1 } }))
  const listKnowledgeTags = t.mock.fn(async (_id: string, _params: unknown) => ({ data: { data: [], total: 0 } }))
  f.component(new URL('./components/FAQEntryManager.vue', import.meta.url), {
    'vue-router': { useRouter: () => ({}) },
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    'tdesign-vue-next': { MessagePlugin: { error: assert.fail } },
    '@/components/settings/useConfirmDelete': { useConfirmDelete: () => ({}) },
    '@/stores/auth': { useAuthStore: () => ({ hasRole: () => true }) },
    '@/stores/ui': { useUIStore: () => ({ clearSelectedTagIds() {} }) },
    '@/stores/organization': { useOrganizationStore: () => ({ sharedKnowledgeBases: [], async fetchSharedKnowledgeBases() {} }) },
    '@/stores/chatResources': { useChatResourcesStore: () => ({ rawKnowledgeBases: [], async ensureKnowledgeBases() {} }) },
    '@/api/knowledge-base': {
      listFAQEntries, listKnowledgeTags,
      getKnowledgeBaseById: async () => ({ data: { id: 'kb', type: 'faq' } }),
    },
    xlsx: {}, papaparse: {},
  }, { kbId: 'kb' })
  await flush()
  listFAQEntries.mock.resetCalls()
  listKnowledgeTags.mock.resetCalls()
  f.emit(change('faq.update_entry', { knowledge_base_id: ['other'] }))
  f.emit(change('agent.create', { knowledge_base_id: ['kb'] }))
  t.mock.timers.tick(300)
  assert.equal(listFAQEntries.mock.callCount(), 0)
  assert.equal(listKnowledgeTags.mock.callCount(), 0)
  f.emit(change('faq.batch_update', { knowledge_base_id: ['kb'] }))
  f.emit(change('faq.delete_entries', { knowledge_base_id: ['kb'] }))
  t.mock.timers.tick(300)
  await flush()
  assert.equal(listFAQEntries.mock.callCount(), 1)
  assert.equal(listKnowledgeTags.mock.callCount(), 1)
  assert.equal(listFAQEntries.mock.calls[0].arguments[0], 'kb')
  assert.equal(listKnowledgeTags.mock.calls[0].arguments[0], 'kb')
  assert.equal((listKnowledgeTags.mock.calls[0].arguments[1] as any).page, 1)
})

test('refreshes Wiki pages, selected content and statistics for its own KB only', async t => {
  const f = fixture(t)
  const page = { id: 'page', slug: 'page', title: 'Updated title', content: '', version: 2 }
  const getWikiPage = t.mock.fn(async (_id: string, _slug: string) => ({ data: page }))
  const getWikiIndex = t.mock.fn(async () => ({ groups: [] }))
  const getWikiStats = t.mock.fn(async () => ({}))
  const { vm } = f.component(new URL('./wiki/WikiBrowser.vue', import.meta.url), {
    marked,
    'vue-router': { useRouter: () => ({}), useRoute: () => ({ query: {} }) },
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    'tdesign-vue-next': { MessagePlugin: { error: assert.fail } },
    '@/stores/assistant': { useAssistantStore: () => ({ visible: false }) },
    '@/composables/useAssistantPageContext': { useAssistantPageContext() {} },
    '@/stores/settings': { useSettingsStore: () => ({}) },
    '@/api/wiki': {
      getWikiPage, getWikiIndex, getWikiStats,
      listWikiIssues: async () => [], listWikiPages: async () => ({ pages: [], total: 0 }),
      listWikiFolders: async () => ({ folders: [] }),
    },
    '@/utils/security': { sanitizeMarkdownHTML: (html: string) => html, hydrateProtectedFileImages: async () => {} },
    '@/utils/markedLiteralTilde': literalTilde,
    'vue-virtual-scroller': {}, '@/api/knowledge-base': {}, '@/api/chat': {},
  }, { knowledgeBaseId: 'kb' })
  await flush()
  vm.selectedPage = { ...page, title: 'Old title' }
  const reloads = [getWikiPage, getWikiIndex, getWikiStats]
  reloads.forEach(reload => reload.mock.resetCalls())
  f.emit(change('wiki.revert', { knowledge_base_id: ['other'] }))
  f.emit(change('agent.create', { knowledge_base_id: ['kb'] }))
  t.mock.timers.tick(300)
  reloads.forEach(reload => assert.equal(reload.mock.callCount(), 0))
  f.emit(change('wiki.revert', { knowledge_base_id: ['kb'] }))
  f.emit(change('wiki.revert', { knowledge_base_id: ['kb'] }))
  t.mock.timers.tick(300)
  await flush()
  reloads.forEach(reload => assert.equal(reload.mock.callCount(), 1))
  assert.deepEqual(Array.from(getWikiPage.mock.calls[0].arguments), ['kb', 'page'])
  assert.equal(vm.selectedPage.title, 'Updated title')
})

test('refreshes the mounted KB list with force for knowledge_base changes only', async t => {
  const f = fixture(t)
  const fetchKnowledgeBasesForList = t.mock.fn(async (_params: unknown, _force: boolean) => [{ id: 'kb', name: 'Updated KB' }])
  const { vm } = f.component(new URL('./KnowledgeBaseList.vue', import.meta.url), {
    'vue-router': { useRoute: () => ({ query: {} }), useRouter: () => ({}) },
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    'tdesign-vue-next': { MessagePlugin: {} },
    '@/stores/auth': { useAuthStore: () => ({ hasRole: () => true }) },
    '@/stores/ui': { useUIStore: () => ({}) },
    '@/stores/organization': { useOrganizationStore: () => ({
      sharedKnowledgeBases: [], async fetchOrganizations() {}, async fetchSharedKnowledgeBases() {},
    }) },
    '@/stores/chatResources': { useChatResourcesStore: () => ({ fetchKnowledgeBasesForList }) },
    '@/components/settings/useConfirmDelete': { useConfirmDelete: () => ({}) },
    '@/composables/useTenantModelReadiness': { useTenantModelReadiness: () => ({ loaded: vue.ref(true), isReadyForDocumentKb: vue.ref(true) }) },
    '@/composables/useListUrlState': { useListUrlState: () => ({ scope: vue.ref('all'), creator: vue.ref('all'), query: vue.ref('') }) },
    '@/composables/useResourcePins': { useResourcePins: () => ({ favorites: vue.ref([]), recents: vue.ref([]) }) },
    '@/api/knowledge-base': {}, '@/api/organization': {}, '@/config/contextualGuides': {},
    '@/utils/index': {}, '@/utils/kbPermission': {},
    '@/utils/resourceListSearch': {}, '@/utils/card-list-badge': {}, '@/utils/resourceSorting': sorting,
  })
  await flush()
  fetchKnowledgeBasesForList.mock.resetCalls()
  f.emit(change('unknown.update'))
  t.mock.timers.tick(300)
  assert.equal(fetchKnowledgeBasesForList.mock.callCount(), 0)
  f.emit(change('knowledge_base.generate_profile'))
  t.mock.timers.tick(300)
  await flush()
  assert.equal(fetchKnowledgeBasesForList.mock.callCount(), 1)
  assert.equal(fetchKnowledgeBasesForList.mock.calls[0].arguments[1], true)
  assert.equal(vm.kbs[0].name, 'Updated KB')
})
