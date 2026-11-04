import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import * as vue from 'vue'
import { fixture, flush } from '../../../test-utils/vueFixture.ts'

function entry(id: number, tag_id = 1) {
  return { id, tag_id, standard_question: `Question ${id}`, is_enabled: id % 2 === 0, answers: ['Answer'] }
}

async function setup(t: TestContext, permission = 'admin') {
  const f = fixture(t)
  const access = vue.reactive({ permission })
  const allEntries = Array.from({ length: 45 }, (_, i) => entry(i + 1, i < 25 ? 1 : 2))
  const listFAQEntries = t.mock.fn(async (_kbId: string, query: any) => {
    let rows = allEntries
    if (query.tag_ids) rows = rows.filter(row => String(row.tag_id) === query.tag_ids)
    if (query.keyword) rows = rows.filter(row => row.standard_question.includes(query.keyword))
    const start = (query.page - 1) * query.page_size
    return { data: { data: rows.slice(start, start + query.page_size), total: rows.length } }
  })
  const messages = { success: t.mock.fn(), error: t.mock.fn(), warning: t.mock.fn() }
  const page = f.component(new URL('./FAQEntryManager.vue', import.meta.url), {
    'vue-router': { useRouter: () => ({ push() {} }) },
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    'tdesign-vue-next': { MessagePlugin: messages },
    '@/components/settings/useConfirmDelete': { useConfirmDelete: () => () => {} },
    '@/stores/auth': { useAuthStore: () => ({ user: { id: 'viewer' }, hasRole: () => false }) },
    '@/stores/organization': { useOrganizationStore: () => ({
      sharedKnowledgeBases: [], fetchSharedKnowledgeBases() {},
      canEditKB: () => ['editor', 'admin'].includes(access.permission),
      canManageKB: () => access.permission === 'admin',
    }) },
    '@/stores/chatResources': { useChatResourcesStore: () => ({ rawKnowledgeBases: [], async ensureKnowledgeBases() {} }) },
    '@/stores/ui': { useUIStore: () => ({ clearSelectedTagIds() {}, toggleSelectedTagId() {} }) },
    '@/api/knowledge-base': {
      listFAQEntries,
      getKnowledgeBaseById: async (id: string) => ({ data: { id, type: 'faq' } }),
      listKnowledgeTags: async () => ({ data: { data: [], total: 0 } }),
    },
    xlsx: {}, papaparse: {},
  }, { kbId: 'faq' })
  await flush()
  assert.equal(messages.error.mock.callCount(), 0)
  return { ...page, access, listFAQEntries }
}

function selected(vm: any): number[] {
  return Array.from(vm.selectedRowKeys)
}

test('batch entry opens with no selection; select loaded covers only loaded pages and can be repeated', async t => {
  const { vm, listFAQEntries } = await setup(t)
  vm.toggleBatchMode()
  assert.equal(vm.batchMode, true)
  assert.deepEqual(selected(vm), [])
  const calls = listFAQEntries.mock.callCount()
  vm.selectLoadedEntries()
  assert.equal(listFAQEntries.mock.callCount(), calls)
  assert.deepEqual(selected(vm), Array.from({ length: 20 }, (_, i) => i + 1))
  assert.equal(vm.selectedEnabledCount, 10)
  assert.equal(vm.selectedDisabledCount, 10)
  await vm.loadEntries(true)
  assert.equal(vm.entries.length, 40)
  assert.equal(vm.selectedRowKeys.length, 20)
  vm.selectLoadedEntries()
  vm.selectLoadedEntries()
  assert.deepEqual(selected(vm), Array.from({ length: 40 }, (_, i) => i + 1))
  vm.exitBatchMode()
  assert.equal(vm.batchMode, false)
  assert.deepEqual(selected(vm), [])
})

test('card selection enters batch mode, keeps selection across views, and toolbar cancel clears it', async t => {
  const { vm } = await setup(t)
  vm.handleCardSelect(1, true)
  assert.equal(vm.batchMode, true)
  vm.viewMode = 'list'
  await flush()
  assert.deepEqual(selected(vm), [1])
  vm.handleCardSelect(2, true)
  vm.handleCardSelect(1, false)
  assert.deepEqual(selected(vm), [2])
  vm.toggleBatchMode()
  assert.equal(vm.batchMode, false)
  assert.deepEqual(selected(vm), [])
})

test('tag and keyword changes clear selection and select only loaded matching entries', async t => {
  const { vm } = await setup(t)
  vm.toggleBatchMode()
  vm.selectLoadedEntries()
  vm.selectedTagIds = ['2']
  await flush()
  assert.deepEqual(selected(vm), [])
  vm.selectLoadedEntries()
  assert.deepEqual(selected(vm), Array.from({ length: 20 }, (_, i) => i + 26))
  vm.entrySearchKeyword = 'Question 4'
  await flush()
  t.mock.timers.tick(300)
  await flush()
  assert.deepEqual(selected(vm), [])
  vm.selectLoadedEntries()
  assert.deepEqual(selected(vm), [40, 41, 42, 43, 44, 45])
})

test('empty loaded results can be selected and cancelled without selecting stale IDs', async t => {
  const { vm } = await setup(t)
  vm.toggleBatchMode()
  vm.entries = []
  vm.selectLoadedEntries()
  assert.deepEqual(selected(vm), [])
  vm.exitBatchMode()
  assert.equal(vm.batchMode, false)
})

test('viewers cannot enter batch mode or select entries; permission loss clears selection', async t => {
  const { vm, access } = await setup(t, 'viewer')
  vm.toggleBatchMode()
  vm.selectLoadedEntries()
  vm.handleCardSelect(1, true)
  assert.equal(vm.batchMode, false)
  assert.deepEqual(selected(vm), [])
  access.permission = 'editor'
  await flush()
  vm.toggleBatchMode()
  vm.selectLoadedEntries()
  assert.equal(vm.selectedRowKeys.length, 20)
  access.permission = 'viewer'
  await flush()
  assert.equal(vm.batchMode, false)
  assert.deepEqual(selected(vm), [])
})

for (const field of ['batchTagLoading', 'batchDeleteLoading', 'batchStatusAction']) {
  test(`selection is frozen during ${field}`, async t => {
    const { vm } = await setup(t)
    vm.handleCardSelect(1, true)
    vm[field] = field === 'batchStatusAction' ? 'disable' : true
    vm.selectLoadedEntries()
    vm.handleCardSelect(2, true)
    vm.handleCardSelect(1, false)
    vm.toggleBatchMode()
    vm.exitBatchMode()
    assert.equal(vm.batchMode, true)
    assert.deepEqual(selected(vm), [1])
  })
}

test('switching knowledge base clears selection and exits batch mode immediately', async t => {
  const { vm } = await setup(t)
  vm.handleCardSelect(1, true)
  vm.$.props.kbId = ''
  await flush()
  assert.equal(vm.batchMode, false)
  assert.deepEqual(selected(vm), [])
})
