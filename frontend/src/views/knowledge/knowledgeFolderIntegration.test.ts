import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

import { buildKnowledgeScopeProjection } from '../../utils/knowledgeScope.ts'
import {
  readKnowledgeFolderSelection,
  writeKnowledgeFolderSelection,
} from './utils/knowledgeFolderState.ts'

const knowledgeBase = readFileSync(new URL('./KnowledgeBase.vue', import.meta.url), 'utf8')
const uiStore = readFileSync(new URL('../../stores/ui.ts', import.meta.url), 'utf8')
const manualEditor = readFileSync(
  new URL('../../components/manual-knowledge-editor.vue', import.meta.url),
  'utf8',
)
const platform = readFileSync(new URL('../platform/index.vue', import.meta.url), 'utf8')
const knowledgeApi = readFileSync(
  new URL('../../api/knowledge-base/index.ts', import.meta.url),
  'utf8',
)

test('keeps all-files, root, and concrete-folder navigation states distinct', () => {
  const baseQuery = { tab: 'documents', keyword: 'guide' }

  assert.equal(readKnowledgeFolderSelection(baseQuery), undefined)
  assert.deepEqual(writeKnowledgeFolderSelection(baseQuery, ''), {
    tab: 'documents',
    keyword: 'guide',
    folder_id: '',
  })
  assert.deepEqual(writeKnowledgeFolderSelection(baseQuery, 'folder-a'), {
    tab: 'documents',
    keyword: 'guide',
    folder_id: 'folder-a',
  })
  assert.deepEqual(baseQuery, { tab: 'documents', keyword: 'guide' })
})

test('projects a folder chat target as canonical recursive folder scope', () => {
  const projection = buildKnowledgeScopeProjection({
    knowledgeBaseIds: [],
    knowledgeIds: [],
    tags: [],
    folderSelections: [{
      knowledgeBaseId: 'kb-a',
      knowledgeBaseName: 'Knowledge Base A',
      folderId: 'folder-a',
      folderName: 'Folder A',
      folderPath: ['Parent', 'Folder A'],
      ancestorFolderIds: ['folder-parent'],
      includeDescendants: true,
    }],
  })

  assert.deepEqual(projection, {
    knowledge_base_ids: ['kb-a'],
    knowledge_scope: {
      folder_scopes: [{
        knowledge_base_id: 'kb-a',
        folder_ids: ['folder-a'],
        include_descendants: true,
      }],
    },
  })
})

test('folder selection drives the server-side knowledge query', () => {
  assert.match(knowledgeBase, /const currentFolderId = computed/)
  assert.match(knowledgeBase, /folder_id:\s*currentFolderId\.value/)
  assert.match(knowledgeBase, /knowledgeListLoadingGeneration/)
  assert.match(knowledgeBase, /requestGeneration === knowledgeListLoadingGeneration/)
  assert.match(knowledgeApi, /params\.folder_id !== undefined/)
  assert.match(knowledgeApi, /query\.append\('folder_id',\s*params\.folder_id\)/)
  assert.match(knowledgeBase, /<KnowledgeFolderTree/)
  assert.match(knowledgeBase, /:refresh-key="folderTreeRefreshKey"/)
  assert.match(knowledgeBase, /refreshKnowledgeFolderTree/)
  assert.doesNotMatch(knowledgeBase, /cardList\.value\.filter\([^)]*folder_id/)
})

test('file and URL uploads use the folder snapshot captured before confirmation', () => {
  assert.match(knowledgeBase, /const uploadFolderId = resolveKnowledgeUploadFolderID\(currentFolderId\.value\)/)
  assert.match(knowledgeBase, /handleUploadConfirmResult\(result,\s*targetKbId,\s*uploadFolderId\)/)
  assert.match(knowledgeBase, /folder_id:\s*folderId/)
  assert.match(knowledgeBase, /detail:\s*{\s*kbId:\s*targetKbId,\s*folderId\s*}/)
  assert.match(knowledgeApi, /folder_id\?:\s*string/)
  assert.doesNotMatch(knowledgeBase, /getFolderUploadFileName/)
  assert.doesNotMatch(knowledgeBase, /webkitRelativePath[\s\S]{0,300}fileName/)
})

test('manual creation keeps its opening folder context', () => {
  assert.match(uiStore, /manualEditorFolderId/)
  assert.match(uiStore, /folderId\?:\s*string/)
  assert.match(manualEditor, /payload\.folder_id\s*=\s*uiStore\.manualEditorFolderId/)
})

test('global knowledge-page drops enter the page upload confirmation flow', () => {
  assert.match(platform, /weknora:knowledge-file-drop/)
  assert.match(knowledgeBase, /weknora:knowledge-file-drop/)
  assert.match(knowledgeBase, /openUploadConfirmDialog\(filtered\.validFiles/)
})

test('folder and knowledge-base chat actions prepare one canonical quick-answer scope', () => {
  const folderHandler = knowledgeBase.match(
    /const handleStartFolderChat[\s\S]*?\n};\n\nconst handleStartKnowledgeBaseChat/,
  )?.[0]
  const knowledgeBaseHandler = knowledgeBase.match(
    /const handleStartKnowledgeBaseChat[\s\S]*?\n};\n\nconst kbInfo/,
  )?.[0]

  assert.ok(folderHandler)
  assert.ok(knowledgeBaseHandler)
  assert.match(folderHandler, /prepareQuickAnswerScope\(\{\s*type: 'folder'/)
  assert.match(folderHandler, /folderId: payload\.folderId/)
  assert.match(folderHandler, /includeDescendants: true/)
  assert.match(folderHandler, /router\.push\(\{ name: 'globalCreatChat' \}\)/)
  assert.match(
    knowledgeBaseHandler,
    /prepareQuickAnswerScope\(\{\s*type: 'knowledge-base'/,
  )
  assert.match(
    knowledgeBaseHandler,
    /knowledgeBaseId: payload\.knowledgeBaseId/,
  )
  assert.match(
    knowledgeBaseHandler,
    /router\.push\(\{ name: 'globalCreatChat' \}\)/,
  )
  assert.doesNotMatch(
    `${folderHandler}\n${knowledgeBaseHandler}`,
    /startStream|createSessions|changeFirstQuery/,
  )
})

test('keeps the original document area without adding pagination', () => {
  assert.doesNotMatch(knowledgeBase, /<t-pagination|<.*pagination/i)
  assert.doesNotMatch(knowledgeBase, /folder-breadcrumb/)
  assert.doesNotMatch(
    knowledgeBase,
    /\.knowledge-main\s*\{[^}]*\bgap:\s*16px/,
  )
  assert.match(knowledgeBase, /<DocumentListView/)
  assert.match(knowledgeBase, /<DocumentCardView/)
})
