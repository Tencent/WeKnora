import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const component = readFileSync(
  new URL('./KnowledgeFolderTree.vue', import.meta.url),
  'utf8',
)
const actions = readFileSync(
  new URL('./KnowledgeFolderActions.vue', import.meta.url),
  'utf8',
)
const folderApi = readFileSync(
  new URL('../../../api/knowledge-folder/index.ts', import.meta.url),
  'utf8',
)
const request = readFileSync(
  new URL('../../../utils/request.ts', import.meta.url),
  'utf8',
)
const zhCN = readFileSync(
  new URL('../../../i18n/locales/zh-CN.ts', import.meta.url),
  'utf8',
)
const enUS = readFileSync(
  new URL('../../../i18n/locales/en-US.ts', import.meta.url),
  'utf8',
)
const koKR = readFileSync(
  new URL('../../../i18n/locales/ko-KR.ts', import.meta.url),
  'utf8',
)
const ruRU = readFileSync(
  new URL('../../../i18n/locales/ru-RU.ts', import.meta.url),
  'utf8',
)

test('keeps the folder panel header title-only with no management icons', () => {
  const header = component.match(
    /<header class="knowledge-folder-tree__header">([\s\S]*?)<\/header>/,
  )?.[1]

  assert.ok(header)
  assert.match(header, /knowledgeFolder\.title/)
  assert.doesNotMatch(header, /KnowledgeFolderActions|<button|name="more"/)
  assert.match(component, /role="separator"/)
})

test('keeps the required actions in row-level three-dot menus', () => {
  assert.match(component, /<KnowledgeFolderActions[\s\S]*?kind="root"/)
  assert.match(component, /<KnowledgeFolderActions[\s\S]*?kind="folder"/)
  assert.match(actions, /name="more"/)
  assert.match(actions, /knowledgeFolder\.createTopLevel/)
  assert.match(actions, /knowledgeFolder\.createChild/)
  assert.match(actions, /knowledgeFolder\.rename/)
  assert.match(actions, /knowledgeFolder\.delete/)
  assert.match(actions, /knowledgeFolder\.startKnowledgeBaseChat/)
  assert.match(actions, /knowledgeFolder\.startFolderChat/)
  assert.match(actions, /v-if="canEdit"/)
})

test('folder chat actions only emit canonical payloads and never send a question', () => {
  assert.match(actions, /emit\('start-chat'\)/)
  assert.match(component, /emit\('start-folder-chat'/)
  assert.match(component, /includeDescendants: true/)
  assert.match(component, /emit\('start-knowledge-base-chat'/)
  assert.doesNotMatch(
    `${component}\n${actions}`,
    /startStream|createSessions|changeFirstQuery/,
  )
})

test('keeps PATCH minimal and used by the rename endpoint', () => {
  assert.match(request, /export function patch<T/)
  assert.match(request, /instance\.patch<T>/)
  assert.match(folderApi, /updateKnowledgeFolder/)
  assert.match(folderApi, /patch<KnowledgeFolderMutationEnvelope>/)
})

test('guards lazy level responses and maps delete conflict without localized text matching', () => {
  const directMessageComparison =
    /(?:^|[^\w])(?:message|[\w?.]+\.message)\s*===\s*['"](?!(?:string|number|object|undefined|boolean|function|symbol|bigint)['"])[^'"\r\n]+['"]/m

  assert.match(component, /parent_id: parentId/)
  assert.match(component, /const requestId = \+\+treeRequestSequence/)
  assert.match(component, /levels\[levelKey\(parentId\)\] !== state/)
  assert.match(component, /state\.requestId !== requestId/)
  assert.match(component, /status === 409/)
  assert.match(component, /knowledgeFolder\.notEmpty/)
  assert.doesNotMatch(
    component,
    /\bmessage(?:\?\.|\.)trim\(\)\s*===\s*['"]/,
  )
  assert.doesNotMatch(
    component,
    directMessageComparison,
  )
  assert.doesNotMatch(
    "typeof candidate.message === 'string'",
    directMessageComparison,
  )
  assert.match(
    "candidate?.message === 'directory is not empty'",
    directMessageComparison,
  )
  assert.doesNotMatch(component, /目录不为空|directory is not empty/i)
})

test('defines the folder action and error copy in every supported locale', () => {
  const requiredKeys = [
    'actions',
    'createTopLevel',
    'createChild',
    'rename',
    'delete',
    'startKnowledgeBaseChat',
    'startFolderChat',
    'folderNamePlaceholder',
    'deleteConfirm',
    'notEmpty',
    'createSuccess',
    'createFailed',
    'renameSuccess',
    'renameFailed',
    'deleteSuccess',
    'deleteFailed',
    'expandFolder',
    'collapseFolder',
  ]

  for (const locale of [zhCN, enUS, koKR, ruRU]) {
    const section = locale.match(
      /knowledgeFolder:\s*{([\s\S]*?)\n  },\n  knowledgeScope:/,
    )?.[1]
    assert.ok(section)
    for (const key of requiredKeys) {
      assert.match(section, new RegExp(`\\b${key}:`))
    }
  }
})
