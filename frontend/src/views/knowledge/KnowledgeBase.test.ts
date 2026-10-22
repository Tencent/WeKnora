import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import * as vue from 'vue'
import * as stall from '../../utils/knowledgeProcessingStall.ts'
import { change, fixture, flush } from '../../test-utils/vueFixture.ts'

async function setup(t: TestContext) {
  const f = fixture(t)
  const route = vue.reactive({ params: { kbId: 'kb' }, query: {} })
  const hook = {
    cardList: vue.ref([{ id: 'doc', parse_status: 'completed' }]), total: vue.ref(1), moreIndex: vue.ref(-1),
    details: vue.reactive({ id: 'doc' }), getKnowled: t.mock.fn(async () => {}),
    getCardDetails: t.mock.fn(), getfDetails() {}, onVisibleChange() {},
  }
  const listKnowledgeFolders = t.mock.fn(async (_id: string) => ({ data: { folders: [] } }))
  const batchReparseKnowledge = t.mock.fn(async () => ({ success: true }))
  const fetchKnowledgeBaseById = t.mock.fn(async (_id: string, _force: boolean) => ({ id: 'kb', type: 'document' }))
  let settle: () => void = () => {}
  const waitForKnowledgeDeletion = t.mock.fn((_ids: string[]) => new Promise<string>((resolve) => {
    settle = () => resolve('completed')
  }))
  const page = f.component(new URL('./KnowledgeBase.vue', import.meta.url), {
    'vue-router': { useRoute: () => route, useRouter: () => ({ replace() {} }) },
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    'tdesign-vue-next': { MessagePlugin: { error() {}, success() {}, warning() {}, info() {} } },
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
    '@/utils/kbPermission': {}, '@/utils/knowledgeDeletion': { waitForKnowledgeDeletion }, '@/utils/knowledgeTrace': {},
    '@/utils/knowledgeProcessingStall': stall, '@/api/wiki': {}, '@/api/knowledge-base': {},
    '@/api/knowledge-base/index': {
      batchReparseKnowledge,
      listKnowledgeFolders,
      listKnowledgeTags: async () => ({ data: { data: [], total: 0 } }),
    },
  })
  await flush()
  page.vm.isCardDetails = true
  hook.getKnowled.mock.resetCalls()
  listKnowledgeFolders.mock.resetCalls()
  fetchKnowledgeBaseById.mock.resetCalls()
  return {
    ...f, ...page, route, hook, batchReparseKnowledge, listKnowledgeFolders, fetchKnowledgeBaseById, waitForKnowledgeDeletion,
    settleDeletion: () => settle(),
  }
}


test('does not reload the document list after a batch reparse is accepted', async t => {
  const f = await setup(t)
  // Finish the initial KB load before adding the currently displayed row.
  // The mocked loader does not populate rows itself.
  await f.vm.loadKnowledgeBaseInfo('kb')
  await flush()
  f.hook.getKnowled.mock.resetCalls()
  f.hook.cardList.value.push({ id: 'doc', parse_status: 'completed' })
  f.vm.selectedIds.add('doc')

  await f.vm.confirmBatchReparse()
  await flush()

  assert.equal(f.batchReparseKnowledge.mock.callCount(), 1)
  assert.equal(f.hook.cardList.value[0].parse_status, 'pending')
  assert.equal(f.hook.getKnowled.mock.callCount(), 0)
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
