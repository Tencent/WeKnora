import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import * as vue from 'vue'
import { fixture, flush } from '../../test-utils/vueFixture.ts'

// The recents/favorites tabs resolve localStorage pin entries against the
// knowledge bases the page can actually see (own + shared) and silently drop
// the ones that are gone — deleted KB, revoked share. The tab counts used to
// be computed from the raw pin entries instead, so after deleting a KB the
// header kept saying 「最近 4」 above a list of 1 (Tencent/WeKnora#5278).
//
// The fixture mounts the real list page with 4 kb pins where only one KB (and
// one shared KB) still resolves, and asserts both counts equal the resolved
// lists. Before the fix kbRecentsCount reported 3 and kbFavoritesCount 2.

function makePins(ids: Array<[string, string]>) {
  return ids.map(([type, id], i) => ({ type, id, ts: 1_700_000_000_000 + i }))
}

async function setup(t: TestContext, kbsByCall: any[][]) {
  const f = fixture(t)
  const route = vue.reactive({ params: {}, query: {} })

  const shared = [{
    knowledge_base: { id: 'kb-shared-live', name: '跨空间共享库', updated_at: '2026-11-02T00:00:00Z' },
    permission: 'viewer', shared_at: '2026-11-02T00:00:00Z', share_id: 'share-1',
    organization_id: 'org-1', org_name: '协作空间',
  }]
  let call = 0

  const pins = {
    favorites: vue.ref(makePins([
      ['kb', 'kb-live'],          // resolves
      ['kb', 'kb-deleted'],       // stale: KB was deleted
      ['agent', 'agent-live'],    // different resource type, not counted here
    ])),
    recents: vue.ref(makePins([
      ['kb', 'kb-live'],          // resolves
      ['kb', 'kb-second'],        // resolves until its deletion snapshot
      ['kb', 'kb-deleted-1'],     // stale
      ['kb', 'kb-shared-live'],   // resolves via the shared-KB index
      ['kb', 'kb-revoked'],       // stale: share revoked
    ])),
    touchRecent: t.mock.fn(),
    toggleFavorite: t.mock.fn(),
    isFavorite: () => false,
    refresh: async () => {},
  }

  const creator = vue.ref('all')
  const page = f.component(new URL('./KnowledgeBaseList.vue', import.meta.url), {
    'vue-router': { useRoute: () => route, useRouter: () => ({ replace() {} }) },
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    'tdesign-vue-next': { MessagePlugin: {} },
    '@/utils/assistantDataChange': { useAssistantDataChange() {} },
    '@/components/settings/useConfirmDelete': { useConfirmDelete: () => ({}) },
    '@/api/knowledge-base': {
      deleteKnowledgeBase: async () => ({}),
      duplicateKnowledgeBase: async () => ({}),
      togglePinKnowledgeBase: async () => ({}),
    },
    '@/stores/chatResources': { useChatResourcesStore: () => ({
      // Each refetch returns the next snapshot, so a later test can model a
      // KB deletion through the component's own reload path.
      fetchKnowledgeBasesForList: async () => kbsByCall[Math.min(call++, kbsByCall.length - 1)],
      invalidateKnowledgeBaseDetail() {},
    }) },
    '@/stores/ui': { useUIStore: () => vue.reactive({
      showKBEditorModal: false, kbEditorMode: 'create', currentKBId: '', kbEditorType: 'document',
      closeKBEditor() {}, openKBSettings() {}, openCreateKB() {},
    }) },
    '@/stores/auth': { useAuthStore: () => ({
      hasRole: () => false, isLiteMode: false, effectiveTenantId: 1,
    }) },
    '@/stores/organization': { useOrganizationStore: () => vue.reactive({
      sharedKnowledgeBases: shared, organizations: [],
      fetchSharedKnowledgeBases: async () => {}, fetchOrganizations: async () => {},
      resourceCounts: undefined, getKBPermission: () => 'owner',
    }) },
    '@/api/organization': { listOrganizationSharedKnowledgeBases: async () => ({ success: true, data: [] }) },
    '@/utils/index': { formatStringDate: (d: Date) => d.toISOString().slice(0, 10) },
    '@/utils/resourceListSearch': { matchesResourceQuery: () => true },
    '@/utils/resourceSorting': { sortResourcesWithinGroups: (items: any[]) => items },
    '@/utils/card-list-badge': { shouldShowResourceOriginBadge: () => false },
    '@/utils/kbPermission': { permissionCanManageKB: () => true },
    '@/config/contextualGuides': { isContextualGuideDone: () => true, markContextualGuideDone() {} },
    '@/composables/useTenantModelReadiness': { useTenantModelReadiness: () => ({
      loaded: vue.ref(true), isReadyForDocumentKb: vue.computed(() => true),
    }) },
    '@/composables/useListUrlState': { useListUrlState: () => ({
      scope: vue.ref('all'), creator, query: vue.ref(''),
    }) },
    '@/composables/useResourcePins': { useResourcePins: () => pins },
  })
  await flush()
  await flush()
  return { ...f, ...page, pins, creator, shared }
}

test('recent and favorite counts match the resolved lists, not the raw pins', async t => {
  const f = await setup(t, [
    [{ id: 'kb-live', name: '存活知识库', updated_at: '2026-11-01T00:00:00Z' }],
  ])

  // The recents view renders the resolved list: one own KB + one shared KB.
  assert.equal(f.vm.recentsList.length, 2)
  assert.equal(f.vm.kbRecentsCount, 2)
  // The raw pin store still holds 5 kb entries — counting those was the bug.
  assert.equal(f.pins.recents.value.filter((e: any) => e.type === 'kb').length, 5)

  // Favorites resolve to exactly one own KB; the deleted one is dropped.
  assert.equal(f.vm.favoritesList.length, 1)
  assert.equal(f.vm.kbFavoritesCount, 1)
})

test('a later deletion moves the list and the count together', async t => {
  // Second refetch snapshot: one of the two own KBs was deleted meanwhile.
  const f = await setup(t, [
    [{ id: 'kb-live', name: '存活知识库', updated_at: '2026-11-01T00:00:00Z' },
     { id: 'kb-second', name: '稍后删除的库', updated_at: '2026-11-03T00:00:00Z' }],
    [{ id: 'kb-live', name: '存活知识库', updated_at: '2026-11-01T00:00:00Z' }],
  ])
  assert.equal(f.vm.recentsList.length, 3)
  assert.equal(f.vm.kbRecentsCount, 3)

  // The creator-filter watcher refetches through the component's own path.
  f.creator.value = 'mine'
  await flush()
  await flush()

  assert.equal(f.vm.recentsList.length, 2)
  assert.equal(f.vm.kbRecentsCount, 2)
})
