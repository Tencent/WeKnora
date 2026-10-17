import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test, { type TestContext } from 'node:test'
import * as vue from 'vue'
import * as stall from '../../utils/knowledgeProcessingStall.ts'
import { fixture, flush } from '../../test-utils/vueFixture.ts'

const source = readFileSync(new URL('./KnowledgeBase.vue', import.meta.url), 'utf8')

// Mirrors the deployed DingTalk sync: a connector now produces multi-level
// folders, so a parent folder can hold 0 documents of its own while its subtree
// holds 5. Reading the direct count there made the page claim it was empty.
const syncedTree = {
  root_document_count: 3,
  total_document_count: 17,
  folders: [
    { path: '培训材料库', name: '培训材料库', document_count: 3, total_count: 17 },
    {
      path: '天诛渗透攻防',
      name: '天诛渗透攻防',
      document_count: 0,
      total_count: 5,
      children: [
        { path: '天诛渗透攻防/FAQ', name: 'FAQ', document_count: 2, total_count: 2 },
        { path: '天诛渗透攻防/售后', name: '售后', document_count: 2, total_count: 2 },
        { path: '天诛渗透攻防/2024使用培训视频', name: '2024使用培训视频', document_count: 1, total_count: 1 },
      ],
    },
    { path: '空目录', name: '空目录', document_count: 0, total_count: 0 },
  ],
}

async function setup(t: TestContext, tree: unknown) {
  const f = fixture(t)
  const route = vue.reactive({ params: { kbId: 'kb' }, query: {} })
  const hook = {
    cardList: vue.ref<any[]>([]), total: vue.ref(0), moreIndex: vue.ref(-1),
    details: vue.reactive({ id: 'doc' }), getKnowled: t.mock.fn(async () => {}),
    getCardDetails: t.mock.fn(), getfDetails() {}, onVisibleChange() {},
  }
  const listKnowledgeFolders = t.mock.fn(async (_id: string) => ({ data: tree }))
  const fetchKnowledgeBaseById = t.mock.fn(async (_id: string, _force: boolean) => ({ id: 'kb', type: 'document' }))
  const page = f.component(new URL('./KnowledgeBase.vue', import.meta.url), {
    'vue-router': { useRoute: () => route, useRouter: () => ({ replace() {} }) },
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    'tdesign-vue-next': { MessagePlugin: {} },
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
    '@/utils/kbPermission': {}, '@/utils/knowledgeDeletion': {}, '@/utils/knowledgeTrace': {},
    '@/utils/knowledgeProcessingStall': stall, '@/api/wiki': {}, '@/api/knowledge-base': {},
    '@/api/knowledge-base/index': { listKnowledgeFolders, listKnowledgeTags: async () => ({ data: { data: [], total: 0 } }) },
  })
  await flush()
  return { ...f, ...page, hook, listKnowledgeFolders }
}

async function selectFolder(f: Awaited<ReturnType<typeof setup>>, path: string) {
  f.vm.selectedFolderPath = path
  await flush()
}

// ── Breadcrumb ──────────────────────────────────────────────────────────────

test('the breadcrumb counts a folder recursively, not by its direct documents', async t => {
  const f = await setup(t, syncedTree)
  // Browsing lists only the folder's own documents, and its parent row holds none.
  f.hook.total.value = 0
  await selectFolder(f, '天诛渗透攻防')
  assert.equal(f.vm.folderDocumentCount, 5)
})

test('the root breadcrumb counts the whole knowledge base', async t => {
  const f = await setup(t, syncedTree)
  f.hook.total.value = 3 // the root-level documents only
  assert.equal(f.vm.selectedFolderPath, '')
  assert.equal(f.vm.folderDocumentCount, 17)
})

test('filtering keeps reporting the flat result count', async t => {
  const f = await setup(t, syncedTree)
  await selectFolder(f, '天诛渗透攻防')
  f.hook.total.value = 2 // two matching documents inside the subtree
  f.vm.docSearchKeyword = 'FAQ'
  await flush()
  assert.equal(f.vm.isFiltering, true)
  assert.equal(f.vm.folderDocumentCount, 2)
})

test('an unloaded folder tree falls back to the loaded document total', async t => {
  const f = await setup(t, null)
  f.hook.total.value = 4
  await selectFolder(f, '天诛渗透攻防')
  assert.equal(f.vm.folderTree, null)
  assert.equal(f.vm.folderDocumentCount, 4)
})

test('a stale folder path falls back to the loaded document total', async t => {
  const f = await setup(t, syncedTree)
  f.hook.total.value = 2
  await selectFolder(f, '已删除/目录')
  assert.equal(f.vm.folderDocumentCount, 2)
})

// ── Empty state ─────────────────────────────────────────────────────────────

test('a folder whose documents live in sub-folders is not called empty', async t => {
  const f = await setup(t, syncedTree)
  await selectFolder(f, '天诛渗透攻防')
  assert.equal(f.vm.folderSubfolderDocumentCount, 5)
  assert.equal(f.vm.folderEmptyState, 'subfolders')
})

test('a folder that really has no documents keeps the plain empty message', async t => {
  const f = await setup(t, syncedTree)
  await selectFolder(f, '空目录')
  assert.equal(f.vm.folderSubfolderDocumentCount, 0)
  assert.equal(f.vm.folderEmptyState, 'folder')
})

test('filtering keeps the no-match empty message', async t => {
  const f = await setup(t, syncedTree)
  await selectFolder(f, '天诛渗透攻防')
  f.vm.docSearchKeyword = 'nothing matches this'
  await flush()
  assert.equal(f.vm.folderEmptyState, 'search')
})

test('a knowledge base without folders keeps the onboarding empty state', async t => {
  const f = await setup(t, { root_document_count: 0, total_document_count: 0, folders: [] })
  assert.equal(f.vm.folderEmptyState, 'none')
})

// The rendered count and message come from the template, which the mounted
// fixture does not render; these assertions keep the wiring honest.
test('the breadcrumb renders the folder count and the flat total while filtering', () => {
  assert.match(source, /\{ count: folderDocumentCount \}/)
  assert.doesNotMatch(source, /\{ count: total \}/)
})

test('the empty state tells sub-folder documents apart from an empty folder', () => {
  const search = source.indexOf("knowledgeBase.folderTree.emptySearch")
  const subfolders = source.indexOf("knowledgeBase.folderTree.emptyFolderWithSubfolders")
  const emptyFolder = source.indexOf("knowledgeBase.folderTree.emptyFolder')")

  assert.ok(search >= 0, 'the no-match message should stay')
  assert.ok(subfolders > search, 'the sub-folder message should follow the no-match one')
  assert.ok(emptyFolder > subfolders, 'the plain empty message should be the last resort')
  assert.match(source, /v-else-if="folderEmptyState === 'subfolders'"/)
  assert.match(source, /emptyFolderWithSubfolders', \{ count: folderSubfolderDocumentCount \}/)
  assert.match(source, /v-else-if="folderEmptyState === 'folder'"/)
})
