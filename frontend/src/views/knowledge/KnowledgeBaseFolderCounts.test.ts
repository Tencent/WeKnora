import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test, { type TestContext } from 'node:test'
import { createI18n } from 'vue-i18n'
import * as vue from 'vue'
import * as stall from '../../utils/knowledgeProcessingStall.ts'
import { fixture, flush } from '../../test-utils/vueFixture.ts'
import enUS from '../../i18n/locales/en-US'
import zhCN from '../../i18n/locales/zh-CN'
import zhTW from '../../i18n/locales/zh-TW'
import jaJP from '../../i18n/locales/ja-JP'
import koKR from '../../i18n/locales/ko-KR'
import ruRU from '../../i18n/locales/ru-RU'
import { pluralRules } from '../../i18n/pluralizationRules'

const source = readFileSync(new URL('./KnowledgeBase.vue', import.meta.url), 'utf8')

// Mirrors the deployed DingTalk sync, which produces multi-level folders:
// 售后 holds 2 documents of its own plus a video sub-folder with 1 more, so its
// direct, sub-folder and recursive numbers are all different. The root has 3
// documents of its own and 5 top-level folders, and the whole base holds 17.
const syncedTree = {
  root_document_count: 3,
  total_document_count: 17,
  folders: [
    { path: '培训材料库', name: '培训材料库', document_count: 3, total_count: 3 },
    {
      path: '天诛渗透攻防',
      name: '天诛渗透攻防',
      document_count: 0,
      total_count: 5,
      children: [
        { path: '天诛渗透攻防/FAQ', name: 'FAQ', document_count: 2, total_count: 2 },
        {
          path: '天诛渗透攻防/售后',
          name: '售后',
          document_count: 2,
          total_count: 3,
          children: [
            {
              path: '天诛渗透攻防/售后/2024使用培训视频',
              name: '2024使用培训视频',
              document_count: 1,
              total_count: 1,
            },
          ],
        },
      ],
    },
    { path: '设计规范', name: '设计规范', document_count: 4, total_count: 4 },
    { path: '历史归档', name: '历史归档', document_count: 2, total_count: 2 },
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

test('the breadcrumb counts the documents and sub-folders the page can show', async t => {
  const f = await setup(t, syncedTree)
  f.hook.total.value = 2 // the two documents directly inside 售后
  await selectFolder(f, '天诛渗透攻防/售后')
  assert.equal(f.vm.folderContentsCount.documents, 2)
  assert.equal(f.vm.folderContentsCount.folders, 1)
  // 3 is the recursive document total (2 direct + 1 in the video sub-folder),
  // which is what used to be shown next to a list of only 2.
  assert.notEqual(f.vm.folderContentsCount.documents, 3)
  // Fed through the real zh-CN bundle, the pair renders as DingTalk does.
  assert.equal(renderFolderContents('zh-CN', f.vm.folderContentsCount), '2 个文档 / 1 个文件夹')
})

test('the root breadcrumb counts the root documents and the top-level folders', async t => {
  const f = await setup(t, syncedTree)
  f.hook.total.value = 3 // the root-level documents only
  assert.equal(f.vm.selectedFolderPath, '')
  assert.equal(f.vm.folderContentsCount.documents, 3)
  assert.equal(f.vm.folderContentsCount.folders, 5)
  // 17 is the whole knowledge base, not what the root list shows.
  assert.notEqual(f.vm.folderContentsCount.documents, 17)
})

test('the sub-folder number is direct children, not the documents below them', async t => {
  const f = await setup(t, syncedTree)
  f.hook.total.value = 0
  await selectFolder(f, '天诛渗透攻防')
  // FAQ and 售后: the two direct sub-folders.
  assert.equal(f.vm.folderContentsCount.folders, 2)
  // The empty state still reports the 5 documents inside those sub-folders.
  assert.equal(f.vm.folderSubfolderDocumentCount, 5)
  assert.equal(f.vm.folderEmptyState, 'subfolders')
})

test('the breadcrumb omits the folder segment when the folder has no sub-folders', async t => {
  const f = await setup(t, syncedTree)
  f.hook.total.value = 2
  await selectFolder(f, '培训材料库')
  assert.equal(f.vm.folderContentsCount.folders, 0)
  assert.equal(f.vm.isFiltering, false)
  assert.equal(f.vm.showFolderContentsCount, false)
  // The same branch feeds the existing single-number message; rendered for
  // real, it cannot contain a "/ 0 folders" tail.
  const i18n = createI18n({ legacy: false, locale: 'zh-CN', messages: { 'zh-CN': zhCN } })
  const text = i18n.global.t('knowledgeBase.documentCount', { count: 2 })
  assert.equal(text, '2 个文档')
  assert.doesNotMatch(text, /文件夹/)
})

test('filtering keeps the flat result count and adds no folder number', async t => {
  const f = await setup(t, syncedTree)
  await selectFolder(f, '天诛渗透攻防/售后')
  f.hook.total.value = 2 // two matching documents inside the subtree
  f.vm.docSearchKeyword = 'FAQ'
  await flush()
  assert.equal(f.vm.isFiltering, true)
  assert.equal(f.vm.folderContentsCount.documents, 2)
  assert.equal(f.vm.folderContentsCount.folders, 0)
})

// ── Sub-folders as list entries ─────────────────────────────────────────────
// Browsing a folder lists its direct children, the way a file manager does.
// The document list and card grid receive exactly this array.

test('the page lists exactly the direct sub-folders of the current folder', async t => {
  const f = await setup(t, syncedTree)
  await selectFolder(f, '天诛渗透攻防')
  assert.deepEqual(
    f.vm.visibleSubFolders.map((node: { path: string }) => node.path),
    ['天诛渗透攻防/FAQ', '天诛渗透攻防/售后'],
  )

  await selectFolder(f, '')
  assert.equal(f.vm.visibleSubFolders.length, 5)
})

test('filtering hides sub-folder entries because it lists matching documents', async t => {
  const f = await setup(t, syncedTree)
  await selectFolder(f, '天诛渗透攻防')
  f.vm.docSearchKeyword = 'FAQ'
  await flush()
  assert.equal(f.vm.isFiltering, true)
  assert.equal(f.vm.visibleSubFolders.length, 0)
})

test('an unloaded folder tree contributes no sub-folder entries', async t => {
  const f = await setup(t, null)
  await selectFolder(f, '天诛渗透攻防')
  assert.equal(f.vm.folderTree, null)
  assert.equal(f.vm.visibleSubFolders.length, 0)
})

test('an unloaded folder tree reports no folders instead of crashing', async t => {
  const f = await setup(t, null)
  f.hook.total.value = 4
  await selectFolder(f, '天诛渗透攻防')
  assert.equal(f.vm.folderTree, null)
  assert.equal(f.vm.folderContentsCount.documents, 4)
  assert.equal(f.vm.folderContentsCount.folders, 0)
})

test('a stale folder path reports no folders instead of crashing', async t => {
  const f = await setup(t, syncedTree)
  f.hook.total.value = 2
  await selectFolder(f, '已删除/目录')
  assert.equal(f.vm.folderContentsCount.documents, 2)
  assert.equal(f.vm.folderContentsCount.folders, 0)
})

// ── Wording ─────────────────────────────────────────────────────────────────
// The rendered numbers are the pair above; these assertions pin the message the
// template feeds them into, so a single-number message cannot come back.
const FOLDER_CONTENTS = 'knowledgeBase.folderTree.folderContentsCount'
const locales = {
  'zh-CN': zhCN, 'zh-TW': zhTW, 'en-US': enUS, 'ja-JP': jaJP, 'ko-KR': koKR, 'ru-RU': ruRU,
}

function renderFolderContents(
  locale: keyof typeof locales,
  counts: { documents: number; folders: number } = { documents: 2, folders: 1 },
): string {
  const i18n = createI18n({ legacy: false, locale, pluralRules, messages: { [locale]: locales[locale] } })
  // `count` is what vue-i18n uses to pick the plural form; the folder number is
  // the noun that changes, so it drives the choice.
  return i18n.global.t(FOLDER_CONTENTS, { ...counts, count: counts.folders })
}

test('every locale renders both numbers as "documents / folders"', () => {
  assert.equal(renderFolderContents('zh-CN'), '2 个文档 / 1 个文件夹')
  assert.equal(renderFolderContents('zh-TW'), '2 個文件 / 1 個資料夾')
  assert.equal(renderFolderContents('en-US'), '2 documents / 1 folder')
  assert.equal(renderFolderContents('ja-JP'), '2 件のドキュメント / 1 個のフォルダ')
  assert.equal(renderFolderContents('ko-KR'), '문서 2개 / 폴더 1개')
  assert.equal(renderFolderContents('ru-RU'), 'Документов: 2 / 1 папка')
})

test('plural locales inflect the folder noun', () => {
  assert.equal(renderFolderContents('en-US', { documents: 2, folders: 2 }), '2 documents / 2 folders')
  assert.equal(renderFolderContents('ru-RU', { documents: 2, folders: 2 }), 'Документов: 2 / 2 папки')
  assert.equal(renderFolderContents('ru-RU', { documents: 2, folders: 5 }), 'Документов: 2 / 5 папок')
  // No plural change in these locales; the number alone changes.
  assert.equal(renderFolderContents('zh-CN', { documents: 2, folders: 2 }), '2 个文档 / 2 个文件夹')
  assert.equal(renderFolderContents('ja-JP', { documents: 1, folders: 1 }), '1 件のドキュメント / 1 個のフォルダ')
  assert.equal(renderFolderContents('ko-KR', { documents: 2, folders: 2 }), '문서 2개 / 폴더 2개')
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
function expectSource(pattern: RegExp) {
  assert.ok(pattern.test(source), `KnowledgeBase.vue should match ${pattern}`)
}

test('the breadcrumb wires both numbers into the two-part message', () => {
  expectSource(/\$t\('knowledgeBase\.folderTree\.folderContentsCount', folderContentsMessage\)/)
  expectSource(
    /\$t\('knowledgeBase\.folderTree\.filteredCount', \{ count: folderContentsCount\.documents \}\)/,
  )
  expectSource(/v-if="isFiltering"/)
  expectSource(/v-else-if="showFolderContentsCount"/)
  // With no sub-folders the two-part message would end in "0 folders", so the
  // breadcrumb falls back to the existing document-only message.
  expectSource(/\$t\('knowledgeBase\.documentCount', \{ count: folderContentsCount\.documents \}\)/)
})

test('the document views receive the current sub-folders', () => {
  const passed = source.match(/:sub-folders="visibleSubFolders"/g) || []
  assert.equal(passed.length, 2, 'both the list and the card grid should receive the folders')
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
