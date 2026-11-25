import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import { fixture, flush } from '../../../test-utils/vueFixture'

interface FakeEntry {
  id: string
  standard_question: string
  answers: string[]
  is_enabled: boolean
}

function entry(id: string, question: string): FakeEntry {
  return { id, standard_question: question, answers: [`answer for ${question}`], is_enabled: true }
}

function pageRes(rows: FakeEntry[], total: number) {
  return { data: { data: rows, total } }
}

type Handler = (page: number, keyword?: string) => Promise<unknown>

function setup(t: TestContext) {
  const f = fixture(t)
  let handler: Handler = async () => pageRes([], 0)
  const api = {
    listFAQEntries: t.mock.fn((_kbId: string, opts: { page: number; keyword?: string }) =>
      handler(opts.page, opts.keyword)),
    listKnowledgeTags: t.mock.fn(async () => pageRes([], 0)),
    getFAQImportProgress: t.mock.fn(async () => ({ data: null })),
    loadKnowledgeInfo: t.mock.fn(async () => ({ id: 'kb-faq-1', type: 'faq' })),
  }
  const { vm } = f.component(
    new URL('./FAQEntryManager.vue', import.meta.url),
    {
      'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
      'tdesign-vue-next': {
        MessagePlugin: { success() {}, error() {}, warning() {}, info() {} },
      },
      'vue-router': { useRouter: () => ({ push() {}, replace() {} }) },
      '@/stores/auth': {
        useAuthStore: () => ({ user: { id: 'u1' }, hasRole: () => true }),
      },
      '@/stores/organization': {
        useOrganizationStore: () => ({
          sharedKnowledgeBases: [],
          canEditKB: () => true,
          canManageKB: () => true,
          fetchSharedKnowledgeBases: async () => {},
        }),
      },
      '@/stores/chatResources': {
        useChatResourcesStore: () => ({
          ensureKnowledgeBases: async () => {},
          rawKnowledgeBases: [],
        }),
      },
      '@/stores/ui': {
        useUIStore: () => ({
          clearSelectedTagIds() {},
          toggleSelectedTagId() {},
          openKBSettings() {},
        }),
      },
      '@/components/settings/useConfirmDelete': { useConfirmDelete: () => ({}) },
      '@/api/knowledge-base': api,
      xlsx: {},
      papaparse: {},
    },
    { kbId: 'kb-faq-1' },
  )
  return { vm, api, setHandler: (h: Handler) => { handler = h } }
}

test('a stale paged response landing after a filter reset is discarded', async (t) => {
  const { vm, api, setHandler } = setup(t)
  const fillers = Array.from({ length: 20 }, (_, i) => entry(`f${i + 1}`, `Filler entry ${i + 1}`))
  setHandler(async (page, keyword) => {
    if (keyword === 'Alpha') return pageRes([entry('a1', 'Alpha entry 01')], 1)
    if (page === 1) return pageRes(fillers, 30)
    return pageRes([], 0)
  })
  // 挂载时的 immediate watcher 已经用默认空实现跑过首次加载，这里用真实分页数据重载一次
  vm.loadEntries()
  await flush()
  assert.equal(vm.entries.length, 20)

  // 在途的"加载更多"（第 2 页）——挂起不返回
  let release!: (value: unknown) => void
  const stale = new Promise((resolve) => { release = resolve })
  setHandler(async (page, keyword) => {
    if (keyword === 'Alpha') return pageRes([entry('a1', 'Alpha entry 01')], 1)
    if (page === 2) return stale
    if (page === 1) return pageRes(fillers, 30)
    return pageRes([], 0)
  })
  const appendPromise = vm.loadEntries(true)
  await flush()
  assert.equal(vm.loadingMore, true)

  // 旧请求未返回时修改过滤条件触发重置加载
  vm.entrySearchKeyword = 'Alpha'
  const resetPromise = vm.loadEntries(false)
  await flush()
  assert.deepEqual(
    vm.entries.map((e: FakeEntry) => e.standard_question),
    ['Alpha entry 01'],
  )
  assert.equal(vm.loadingMore, false)

  // 旧分页响应晚落地：不得污染过滤结果、不得追加重复条目
  release(pageRes([entry('f10', 'Filler entry 10'), entry('a1b', 'Alpha entry 01')], 30))
  await appendPromise
  await flush()
  assert.deepEqual(
    vm.entries.map((e: FakeEntry) => e.standard_question),
    ['Alpha entry 01'],
  )
  assert.equal(vm.hasMore, false)

  // 旧响应的 finally 不得触发自动填充/翻页：请求总数保持不变
  const callCount = api.listFAQEntries.mock.callCount()
  await flush()
  await flush()
  assert.equal(api.listFAQEntries.mock.callCount(), callCount)
  await resetPromise
})
