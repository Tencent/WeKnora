import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import * as vue from 'vue'
import * as stall from '../../utils/knowledgeProcessingStall.ts'
import { fixture, flush } from '../../test-utils/vueFixture.ts'

type Tag = { id: string; name: string }
type TagResponse = { data: { data: Tag[]; total: number } }
type TagQuery = { page: number; page_size: number; keyword?: string }

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: Error) => void
  const promise = new Promise<T>((accept, fail) => { resolve = accept; reject = fail })
  return { promise, resolve, reject }
}

const tag = (id: string, name = id): Tag => ({ id, name })
const plain = <T>(value: T): T => JSON.parse(JSON.stringify(value))

async function setup(t: TestContext, initialKbId = 'kb-a') {
  const f = fixture(t)
  const route = vue.reactive({ params: { kbId: initialKbId }, query: {} })
  const cards = vue.ref<any[]>([])
  const total = vue.ref(0)
  const docCalls: Array<{ query: any; kb: string }> = []
  const getKnowled = t.mock.fn(async (query: any, kb: string) => {
    docCalls.push({ query: { ...query }, kb })
  })
  const hook = {
    cardList: cards, total, moreIndex: vue.ref(-1), details: vue.reactive({ id: '' }),
    getKnowled, getCardDetails() {}, getfDetails() {}, onVisibleChange() {},
  }
  const tagRequests: Array<{ kb: string; params: TagQuery; d: ReturnType<typeof deferred<TagResponse>> }> = []
  const listKnowledgeTags = t.mock.fn((kb: string, params: TagQuery) => {
    const d = deferred<TagResponse>()
    tagRequests.push({ kb, params: { ...params }, d })
    return d.promise
  })
  const page = f.component(new URL('./KnowledgeBase.vue', import.meta.url), {
    'vue-router': { useRoute: () => route, useRouter: () => ({ replace() {} }) },
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    'tdesign-vue-next': { MessagePlugin: { success: t.mock.fn(), error: t.mock.fn(), warning: t.mock.fn(), info: t.mock.fn() } },
    '@/hooks/useKnowledgeBase': () => hook,
    '@/stores/ui': { useUIStore: () => ({ clearSelectedTagIds() {}, toggleSelectedTagId() {} }) },
    '@/stores/organization': { useOrganizationStore: () => ({ sharedKnowledgeBases: [], getKBPermission: () => 'owner' }) },
    '@/stores/auth': { useAuthStore: () => ({ hasRole: () => true }) },
    '@/stores/chatResources': { useChatResourcesStore: () => ({
      fetchKnowledgeBaseById: async (id: string) => ({ id, type: 'document' }),
      rawKnowledgeBases: [], async ensureKnowledgeBases() {},
    }) },
    '@/stores/editorResources': { useEditorResourcesStore: () => ({ parserEngines: [], async ensureParserEngines() {} }) },
    '@/stores/uploadConfirm': { useUploadConfirmStore: () => ({}) },
    '@/stores/uploadTasks': { useUploadTasksStore: () => ({}) },
    '@/hooks/useMarqueeSelect': { useMarqueeSelect: () => ({}) },
    '@/composables/useAssistantPageContext': { useAssistantPageContext() {} },
    '@/utils/kbPermission': { permissionCanEditKB: () => true, permissionCanManageKB: () => true },
    '@/utils/knowledgeDeletion': {}, '@/utils/knowledgeTrace': {},
    '@/utils/knowledgeProcessingStall': stall, '@/api/wiki': {}, '@/api/knowledge-base': {},
    '@/api/knowledge-base/index': {
      batchQueryKnowledge: async () => ({ success: true, data: [] }),
      batchReparseKnowledge: async () => ({ success: true }),
      listKnowledgeFolders: async () => ({ data: { folders: [] } }),
      listKnowledgeTags,
    },
  })
  await flush()
  // Resolve initial tag load triggered by loadKnowledgeBaseInfo
  assert.equal(tagRequests.length, 1)
  tagRequests[0].d.resolve({ data: { data: [tag('seed')], total: 1 } })
  await flush()

  return {
    vm: page.vm,
    close: page.close,
    route,
    tagRequests,
    docCalls,
    async resolveTag(index: number, rows: Tag[], totalCount = rows.length) {
      assert.ok(tagRequests[index], `expected tag request #${index}`)
      tagRequests[index].d.resolve({ data: { data: rows, total: totalCount } })
      await flush()
    },
    async rejectTag(index: number) {
      assert.ok(tagRequests[index], `expected tag request #${index}`)
      tagRequests[index].d.reject(new Error('tag query failed'))
      await flush()
    },
    async tick(ms = 300) {
      t.mock.timers.tick(ms)
      await flush()
    },
  }
}

test('ignores out-of-order tag search responses and preserves newer loading state', async (t) => {
  const s = await setup(t)
  s.vm.tagSearchQuery = 'alpha'
  await flush()
  await s.tick(300)
  assert.equal(s.tagRequests.length, 2)
  assert.equal(s.tagRequests[1].params.keyword, 'alpha')

  s.vm.tagSearchQuery = 'beta'
  await flush()
  await s.tick(300)
  assert.equal(s.tagRequests.length, 3)
  assert.equal(s.tagRequests[2].params.keyword, 'beta')
  assert.equal(s.vm.tagLoading, true)

  // Stale alpha response arrives while beta is still loading
  await s.resolveTag(1, [tag('a1', 'alpha-1')], 80)
  assert.deepEqual(plain(s.vm.tagList), [])
  assert.equal(s.vm.tagLoading, true)
  assert.equal(s.vm.tagHasMore, false)

  // Current beta response arrives
  await s.resolveTag(2, [tag('b1', 'beta-1')], 1)
  assert.deepEqual(plain(s.vm.tagList), [tag('b1', 'beta-1')])
  assert.equal(s.vm.tagTotal, 1)
  assert.equal(s.vm.tagLoading, false)
})

test('ignores a stale tag response arriving during the debounce window', async (t) => {
  const s = await setup(t)
  s.vm.tagSearchQuery = 'alpha'
  await flush()
  await s.tick(300)
  assert.equal(s.tagRequests.length, 2)

  // User types beta; debounce has not fired yet, so alpha must already be invalid
  s.vm.tagSearchQuery = 'beta'
  await flush()
  await s.resolveTag(1, [tag('a1', 'alpha-1')], 1)
  assert.deepEqual(plain(s.vm.tagList), [])
  assert.equal(s.vm.tagTotal, 0)

  await s.tick(300)
  assert.equal(s.tagRequests.length, 3)
  await s.resolveTag(2, [tag('b1', 'beta-1')], 1)
  assert.deepEqual(plain(s.vm.tagList), [tag('b1', 'beta-1')])
})

test('invalidates an in-flight Load More when a new tag search starts', async (t) => {
  const s = await setup(t)
  const resetPromise = s.vm.loadTags('kb-a', true)
  await flush()
  await s.resolveTag(1, [tag('t1')], 80)
  await resetPromise
  assert.equal(s.vm.tagHasMore, true)
  assert.equal(s.vm.tagPage, 2)

  // Start Load More (page 2)
  const morePromise = s.vm.loadTags('kb-a')
  assert.equal(s.tagRequests.length, 3)
  assert.equal(s.tagRequests[2].params.page, 2)
  assert.equal(s.vm.tagLoadingMore, true)

  // User changes query while page 2 is still pending
  s.vm.tagSearchQuery = 'narrow'
  await flush()
  assert.equal(s.vm.tagLoadingMore, false)

  await s.resolveTag(2, [tag('stale-p2')], 80)
  await morePromise
  assert.deepEqual(plain(s.vm.tagList), [tag('t1')])

  await s.tick(300)
  assert.equal(s.tagRequests.length, 4)
  await s.resolveTag(3, [tag('n1', 'narrow-1')], 1)
  assert.deepEqual(plain(s.vm.tagList), [tag('n1', 'narrow-1')])
  assert.equal(s.vm.tagPage, 1)
})

test('ignores stale tag errors when a newer search is active', async (t) => {
  const s = await setup(t)
  s.vm.tagSearchQuery = 'alpha'
  await flush()
  await s.tick(300)

  s.vm.tagSearchQuery = 'beta'
  await flush()
  await s.tick(300)
  assert.equal(s.vm.tagLoading, true)

  await s.rejectTag(1)
  assert.equal(s.vm.tagLoading, true)

  await s.resolveTag(2, [tag('b1')], 1)
  assert.deepEqual(plain(s.vm.tagList), [tag('b1')])
  assert.equal(s.vm.tagLoading, false)
})

test('does not cancel the new KB tag load when the switch clears the search box', async (t) => {
  const s = await setup(t)
  s.vm.tagSearchQuery = 'alpha'
  await flush()
  await s.tick(300)
  assert.equal(s.tagRequests.length, 2)

  s.route.params.kbId = 'kb-b'
  await flush()
  assert.equal(s.vm.tagSearchQuery, '')
  assert.equal(s.tagRequests.length, 3)
  assert.equal(s.tagRequests[2].kb, 'kb-b')
  assert.equal(s.tagRequests[2].params.keyword, undefined)

  await s.resolveTag(1, [tag('a1', 'alpha-1')], 1)
  await s.resolveTag(2, [tag('b1', 'kb-b-1')], 1)
  assert.deepEqual(plain(s.vm.tagList), [tag('b1', 'kb-b-1')])
  assert.equal(s.vm.tagLoading, false)

  await s.tick(350)
  assert.equal(s.tagRequests.length, 3)
})

test('cancels pending search debounce timers on KB switch', async (t) => {
  const s = await setup(t)
  s.docCalls.length = 0
  s.vm.tagSearchQuery = 'pending-tag'
  s.vm.docSearchKeyword = 'pending-doc'
  await flush()

  // Switch KB before 300ms debounce fires
  s.route.params.kbId = 'kb-b'
  await flush()
  const tagsAfterSwitch = s.tagRequests.length
  const docsAfterSwitch = s.docCalls.length

  await s.tick(350)
  assert.equal(s.tagRequests.length, tagsAfterSwitch)
  assert.equal(s.docCalls.length, docsAfterSwitch)
})

test('cancels pending search debounce timers on unmount', async (t) => {
  const s = await setup(t)
  s.docCalls.length = 0
  s.vm.tagSearchQuery = 'pending-tag'
  s.vm.docSearchKeyword = 'pending-doc'
  await flush()

  // Unmount before 300ms debounce fires
  s.close()
  await s.tick(350)
  assert.equal(s.tagRequests.length, 1)
  assert.equal(s.docCalls.length, 0)
})
