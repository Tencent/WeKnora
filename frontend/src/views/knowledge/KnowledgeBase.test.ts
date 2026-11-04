import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import * as vue from 'vue'
import * as stall from '../../utils/knowledgeProcessingStall.ts'
import { change, fixture, flush } from '../../test-utils/vueFixture.ts'

async function setup(t: TestContext, translate = (key: string, _params?: unknown) => key) {
  const f = fixture(t)
  const route = vue.reactive({ params: { kbId: 'kb' }, query: {} })
  const hook = {
    cardList: vue.ref([{ id: 'doc', parse_status: 'completed' }]), total: vue.ref(1), moreIndex: vue.ref(-1),
    details: vue.reactive({ id: 'doc' }), getKnowled: t.mock.fn(async () => {}),
    getCardDetails: t.mock.fn(), getfDetails() {}, onVisibleChange() {},
  }
  const listKnowledgeFolders = t.mock.fn(async (_id: string) => ({ data: { folders: [] } }))
  const batchQueryKnowledge = t.mock.fn(async (query: string, _kbId?: string) => {
    const ids = new URLSearchParams(query).getAll('ids')
    return { success: true, data: hook.cardList.value.filter(card => ids.includes(card.id)).map(card => ({ ...card })) }
  })
  const fetchKnowledgeBaseById = t.mock.fn(async (_id: string, _force: boolean) => ({ id: 'kb', type: 'document' }))
  let settle: () => void = () => {}
  const waitForKnowledgeDeletion = t.mock.fn((_ids: string[]) => new Promise<string>((resolve) => {
    settle = () => resolve('completed')
  }))
  const page = f.component(new URL('./KnowledgeBase.vue', import.meta.url), {
    'vue-router': { useRoute: () => route, useRouter: () => ({ replace() {} }) },
    'vue-i18n': { useI18n: () => ({ t: translate }) },
    'tdesign-vue-next': { MessagePlugin: { success() {}, error() {}, info() {}, warning() {} } },
    '@/hooks/useKnowledgeBase': () => hook,
    '@/stores/ui': { useUIStore: () => ({ clearSelectedTagIds() {} }) },
    '@/stores/organization': { useOrganizationStore: () => ({ sharedKnowledgeBases: [], getKBPermission: () => 'owner' }) },
    '@/stores/auth': { useAuthStore: () => ({ hasRole: () => true }) },
    '@/stores/chatResources': { useChatResourcesStore: () => ({
      fetchKnowledgeBaseById, rawKnowledgeBases: [], async ensureKnowledgeBases() {},
    }) },
    '@/stores/editorResources': { useEditorResourcesStore: () => ({ parserEngines: [], async ensureParserEngines() {} }) },
    '@/stores/uploadConfirm': { useUploadConfirmStore: () => ({}) },
    '@/stores/uploadTasks': { useUploadTasksStore: () => ({}) },
    '@/hooks/useMarqueeSelect': { useMarqueeSelect: () => ({}) },
    '@/composables/useAssistantPageContext': { useAssistantPageContext() {} },
    '@/utils/kbPermission': { permissionCanEditKB: () => true, permissionCanManageKB: () => true },
    '@/utils/knowledgeDeletion': { waitForKnowledgeDeletion }, '@/utils/knowledgeTrace': {},
    '@/utils/knowledgeProcessingStall': stall, '@/api/wiki': {}, '@/api/knowledge-base': {},
    '@/api/knowledge-base/index': {
      listKnowledgeFolders, listKnowledgeTags: async () => ({ data: { data: [], total: 0 } }),
      batchReparseKnowledge: async () => ({ success: true }),
      batchQueryKnowledge,
    },
  })
  await flush()
  page.vm.isCardDetails = true
  hook.getKnowled.mock.resetCalls()
  listKnowledgeFolders.mock.resetCalls()
  fetchKnowledgeBaseById.mock.resetCalls()
  return {
    ...f, ...page, route, hook, listKnowledgeFolders, fetchKnowledgeBaseById, waitForKnowledgeDeletion, batchQueryKnowledge,
    settleDeletion: () => settle(),
  }
}

test('batch assistant actions include only explicitly selected IDs and disable single-document comparison', async t => {
  const f = await setup(t, (key, params) => JSON.stringify({ key, params }))
  f.hook.cardList.value = ['selected-a', 'selected-b', 'unselected'].map(id => ({ id, parse_status: 'completed' }))
  await flush()
  f.vm.selectedIds = new Set(['selected-a', 'selected-b'])
  const actions = f.vm.selectedDocumentAssistantActions
  for (const action of actions) {
    const params = JSON.parse(action.query).params
    assert.equal(params.kb, 'kb')
    assert.deepEqual(JSON.parse(params.ids), ['selected-a', 'selected-b'])
  }
  assert.equal(actions.find((action: any) => action.key === 'compare').disabled, false)
  f.vm.selectedIds = new Set(['selected-a'])
  assert.equal(f.vm.selectedDocumentAssistantActions.find((action: any) => action.key === 'compare').disabled, true)
  assert.deepEqual(JSON.parse(JSON.parse(actions[0].query).params.ids), ['selected-a', 'selected-b'])
})

test('refreshes files and folders for the open KB and ignores unrelated areas and KBs', async t => {
  const f = await setup(t)
  f.emit(change('knowledge.create_manual', { knowledge_base_id: ['other'] }))
  f.emit(change('agent.create'))
  t.mock.timers.tick(300)
  assert.equal(f.hook.getKnowled.mock.callCount(), 0)
  f.emit(change('knowledge.create_manual', { knowledge_base_id: ['kb'] }))
  f.emit(change('knowledge.rename_folder', { knowledge_base_id: ['kb'] }))
  t.mock.timers.tick(300)
  await flush()
  assert.equal(f.hook.getKnowled.mock.callCount(), 1)
  assert.equal(f.listKnowledgeFolders.mock.callCount(), 1)
  assert.equal(f.listKnowledgeFolders.mock.calls[0].arguments[0], 'kb')
  assert.equal(f.hook.getCardDetails.mock.callCount(), 0)
})

test('reloads KB metadata with force without adding file requests beyond the existing loader', async t => {
  const f = await setup(t)
  await f.vm.loadKnowledgeBaseInfo('kb', true)
  await flush()
  const fileRequests = f.hook.getKnowled.mock.callCount()
  f.hook.getKnowled.mock.resetCalls()
  f.fetchKnowledgeBaseById.mock.resetCalls()
  f.emit(change('knowledge_base.generate_profile', { knowledge_base_id: ['kb'] }))
  t.mock.timers.tick(300)
  await flush()
  assert.deepEqual(Array.from(f.fetchKnowledgeBaseById.mock.calls[0].arguments), ['kb', true])
  assert.equal(f.fetchKnowledgeBaseById.mock.callCount(), 1)
  assert.equal(f.hook.getKnowled.mock.callCount(), fileRequests)
})

for (const operation of ['knowledge.update', 'knowledge.reparse', 'knowledge.cancel_parse', 'chunk.update', 'chunk.revert']) {
  test(`refreshes the open document on ${operation}`, async t => {
    const f = await setup(t)
    f.emit(change(operation, { knowledge_id: ['doc'] }))
    t.mock.timers.tick(300)
    await flush()
    assert.equal(f.hook.getCardDetails.mock.callCount(), 1)
    assert.deepEqual(structuredClone(f.hook.getCardDetails.mock.calls[0].arguments[0]), { id: 'doc' })
    assert.equal(f.hook.getKnowled.mock.callCount(), 1)
    assert.equal(f.vm.isCardDetails, true)
  })
}

test('ignores chunk changes in another document outside this knowledge base', async t => {
  const f = await setup(t)
  f.emit(change('chunk.update', { knowledge_id: ['other-doc'] }))
  t.mock.timers.tick(300)
  assert.equal(f.hook.getCardDetails.mock.callCount(), 0)
  assert.equal(f.hook.getKnowled.mock.callCount(), 0)
})

test('closes a deleted drawer even when the same burst includes an update', async t => {
  const f = await setup(t)
  f.emit(change('knowledge.update', { knowledge_id: ['doc'] }))
  f.emit(change('knowledge.batch_delete', { knowledge_base_id: ['kb'], knowledge_id: ['doc'] }))
  t.mock.timers.tick(300)
  assert.equal(f.vm.isCardDetails, false)
  assert.equal(f.hook.getCardDetails.mock.callCount(), 0)
})

test('refreshes ID-only changes outside the loaded page without refreshing an unrelated drawer', async t => {
  const f = await setup(t)
  f.emit(change('knowledge.delete', { knowledge_id: ['unloaded-doc'] }))
  t.mock.timers.tick(300)
  assert.equal(f.hook.getKnowled.mock.callCount(), 1)
  assert.equal(f.hook.getCardDetails.mock.callCount(), 0)
  assert.equal(f.vm.isCardDetails, true)
})

test('reloads the list again once a delete the assistant started has finished', async t => {
  const f = await setup(t)
  f.emit(change('knowledge.delete', { knowledge_id: ['doc'] }))
  t.mock.timers.tick(300)
  await flush()
  assert.equal(f.vm.isCardDetails, false)
  assert.equal(f.hook.getKnowled.mock.callCount(), 1)
  assert.deepEqual(Array.from(f.waitForKnowledgeDeletion.mock.calls[0].arguments[0] as string[]), ['doc'])
  f.settleDeletion()
  await flush()
  assert.equal(f.hook.getKnowled.mock.callCount(), 2)
})

test('uses the current route when a queued change fires', async t => {
  const f = await setup(t)
  f.emit(change('knowledge.create_manual', { knowledge_base_id: ['kb'] }))
  f.route.params.kbId = 'other'
  await flush()
  f.hook.getKnowled.mock.resetCalls()
  f.vm.isCardDetails = false
  t.mock.timers.tick(300)
  assert.equal(f.hook.getKnowled.mock.callCount(), 0)
})

test('batch reparse preserves loaded pages and updates statuses without reloading the list', async t => {
  const f = await setup(t)
  f.hook.cardList.value = Array.from({ length: 70 }, (_, i) => ({ id: `doc-${i}`, parse_status: 'completed' }))
  await flush()
  const cards = f.hook.cardList.value
  f.vm.selectedIds = new Set(['doc-69'])
  await f.vm.confirmBatchReparse()
  await flush()
  assert.equal(cards[69].parse_status, 'pending')
  t.mock.timers.tick(1500)
  await flush()
  assert.equal(f.vm.pendingReparseAck.has('doc-69'), false)
  assert.equal(f.batchQueryKnowledge.mock.calls[0].arguments[0], 'ids=doc-69')
  assert.equal(f.batchQueryKnowledge.mock.calls[0].arguments[1], 'kb')
  assert.equal(f.hook.getKnowled.mock.callCount(), 0)
  assert.equal(f.hook.cardList.value, cards)
  assert.equal(cards.length, 70)
  assert.equal(f.vm.docListLoading, false)
})

test('an older batch acknowledgement timeout does not clear a newer batch', async t => {
  const f = await setup(t)
  f.hook.cardList.value = [
    { id: 'older', parse_status: 'completed' },
    { id: 'newer', parse_status: 'completed' },
  ]
  f.batchQueryKnowledge.mock.mockImplementation(async (query: string) => ({
    success: true,
    data: new URLSearchParams(query).getAll('ids').map(id => ({ id, parse_status: 'completed' })),
  }))
  f.vm.selectedIds = new Set(['older'])
  await f.vm.confirmBatchReparse()
  await flush()
  for (let i = 0; i < 15; i++) {
    t.mock.timers.tick(400)
    await flush()
  }
  f.vm.selectedIds = new Set(['newer'])
  await f.vm.confirmBatchReparse()
  await flush()
  for (let i = 0; i < 15; i++) {
    t.mock.timers.tick(400)
    await flush()
  }
  assert.equal(f.vm.pendingReparseAck.has('older'), false)
  assert.equal(f.vm.pendingReparseAck.has('newer'), true)
  assert.equal(f.hook.getKnowled.mock.callCount(), 0)
})

test('batch acknowledgement waits stop once their own IDs are acknowledged', async t => {
  const f = await setup(t)
  f.hook.cardList.value = [
    { id: 'older', parse_status: 'completed' },
    { id: 'newer', parse_status: 'completed' },
  ]
  f.batchQueryKnowledge.mock.mockImplementation(async () => ({
    success: true,
    data: [
      { id: 'older', parse_status: 'processing' },
      { id: 'newer', parse_status: 'completed' },
    ],
  }))
  f.vm.selectedIds = new Set(['older', 'newer'])
  await f.vm.confirmBatchReparse()
  await flush()
  assert.equal(f.vm.pendingReparseAck.has('older'), true)
  assert.equal(f.vm.pendingReparseAck.has('newer'), true)
  t.mock.timers.tick(1500)
  await flush()
  assert.equal(f.vm.pendingReparseAck.has('older'), false)
  assert.equal(f.vm.pendingReparseAck.has('newer'), true)
  assert.equal(f.hook.cardList.value[0].parse_status, 'processing')
  assert.equal(f.hook.cardList.value[1].parse_status, 'pending')
  assert.equal(f.hook.getKnowled.mock.callCount(), 0)
})

test('accepts a rebuild completed between polls after the bounded acknowledgement window', async t => {
  const f = await setup(t)
  f.hook.cardList.value = [{ id: 'doc', parse_status: 'completed' }]
  f.batchQueryKnowledge.mock.mockImplementation(async () => ({
    success: true, data: [{ id: 'doc', parse_status: 'completed' }],
  }))
  f.vm.selectedIds = new Set(['doc'])
  await f.vm.confirmBatchReparse()
  await flush()
  for (let i = 0; i < 29; i++) {
    t.mock.timers.tick(400)
    await flush()
  }
  assert.equal(f.hook.cardList.value[0].parse_status, 'pending')
  t.mock.timers.tick(400)
  await flush()
  assert.equal(f.vm.pendingReparseAck.has('doc'), false)
  t.mock.timers.tick(1500)
  await flush()
  assert.equal(f.hook.cardList.value[0].parse_status, 'completed')
  assert.equal(f.hook.getKnowled.mock.callCount(), 0)
})

test('a pending rebuild does not reload another folder or KB after navigation or unmount', async t => {
  const f = await setup(t)
  f.hook.cardList.value = [{ id: 'doc', parse_status: 'completed' }]
  f.vm.selectedIds = new Set(['doc'])
  await f.vm.confirmBatchReparse()
  await flush()
  f.vm.selectedFolderPath = '/other-folder'
  await flush()
  f.route.params.kbId = 'other'
  await flush()
  f.hook.getKnowled.mock.resetCalls()
  for (let i = 0; i < 5; i++) {
    t.mock.timers.tick(400)
    await flush()
  }
  f.close()
  for (let i = 0; i < 30; i++) {
    t.mock.timers.tick(400)
    await flush()
  }
  assert.equal(f.hook.getKnowled.mock.callCount(), 0)
})
