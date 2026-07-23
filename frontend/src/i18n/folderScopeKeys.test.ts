import assert from 'node:assert/strict'
import test from 'node:test'

import zhCN from './locales/zh-CN.ts'
import enUS from './locales/en-US.ts'
import koKR from './locales/ko-KR.ts'
import ruRU from './locales/ru-RU.ts'

const locales = {
  'zh-CN': zhCN,
  'en-US': enUS,
  'ko-KR': koKR,
  'ru-RU': ruRU,
}

const expectedKeys = [
  'entireKnowledgeBase',
  'entireKnowledgeBaseHint',
  'selectFolderForKb',
  'selectedFolderSingle',
  'selectedFolderCount',
  'removeCurrentFolder',
  'loading',
  'loadingShort',
  'loadFailed',
  'loadFailedShort',
  'partialInvalid',
  'partialInvalidShort',
  'noFolders',
  'searchPlaceholder',
  'toggleFolder',
  'sendBlockedInvalid',
]

test('folder scope i18n keys are complete in every locale', () => {
  for (const [locale, messages] of Object.entries(locales)) {
    const scope = (messages as any).input?.folderScope
    assert.ok(scope, `${locale} missing input.folderScope`)
    assert.deepEqual(Object.keys(scope).sort(), expectedKeys.slice().sort(), locale)
    for (const key of expectedKeys) {
      assert.equal(typeof scope[key], 'string', `${locale}.${key}`)
      assert.notEqual(scope[key].trim(), '', `${locale}.${key}`)
    }
  }
})

test('entire knowledge base wording is distinct from root folder wording', () => {
  assert.equal((zhCN as any).input.folderScope.entireKnowledgeBase.includes('根目录'), false)
  assert.equal((enUS as any).input.folderScope.entireKnowledgeBase.toLowerCase().includes('root'), false)
})

test('knowledge folder sidebar resize labels are complete in every locale', () => {
  for (const [locale, messages] of Object.entries(locales)) {
    const knowledgeFolder = (messages as any).knowledgeFolder
    for (const key of ['resizeSidebar', 'resizeSidebarHint']) {
      assert.equal(typeof knowledgeFolder?.[key], 'string', `${locale}.${key}`)
      assert.notEqual(knowledgeFolder[key].trim(), '', `${locale}.${key}`)
    }
  }
})
