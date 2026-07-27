import assert from 'node:assert/strict'
import test from 'node:test'

import enUS from './en-US.ts'
import koKR from './ko-KR.ts'
import ruRU from './ru-RU.ts'
import zhCN from './zh-CN.ts'

const knowledgeFolderKeys = [
  'rootFolder',
  'folderTreeTitle',
  'showFolderTree',
  'hideFolderTree',
  'searchWholeKb',
  'folderBreadcrumbLabel',
  'breadcrumbCollapsedItems',
  'emptyFolderTree',
  'emptyFolderTreeHint',
  'searchFiltersApplyOnlyToDocs',
  'emptyFolderHint',
  'searchNoResults',
  'emptyRootViewer',
  'newFolder',
  'folderActions',
  'folderActionAddSubfolder',
  'folderActionRename',
  'folderActionDelete',
  'folderActionMoveTo',
  'folderActionDeleteConfirm',
  'folderDocumentCount',
  'folderCardType',
  'folderNameRequired',
  'folderNameInvalid',
  'folderNameConflict',
  'folderDepthExceeded',
  'folderActionFailed',
  'expandFolder',
  'collapseFolder',
  'transferToKnowledgeBase',
  'folderPickerTitle',
  'folderPickerTargetLabel',
  'folderPickerSelectHint',
  'folderPickerDisabledHint',
  'folderPickerConfirm',
  'folderPickerEmpty',
  'folderMoveSuccess',
  'folderMoveFailed',
  'folderDeleteTitle',
  'folderDeleteEmptyConfirm',
  'folderDeleteRecursiveConfirm',
  'folderDeleteImpact',
  'folderDeleteImpactFolders',
  'folderDeleteAsyncNote',
  'folderDeleteSubmitted',
  'folderDeleteFailed',
  'folderReparseLimit',
  'reparseFolderUnsupported',
  'batchMoveFolder',
] as const

type KnowledgeFolderKey = (typeof knowledgeFolderKeys)[number]
type KnowledgeBaseLocale = Record<KnowledgeFolderKey, string>

const locales = {
  'zh-CN': zhCN.knowledgeBase,
  'en-US': enUS.knowledgeBase,
  'ko-KR': koKR.knowledgeBase,
  'ru-RU': ruRU.knowledgeBase,
} as const

function placeholders(value: string): string[] {
  return [...value.matchAll(/\{([A-Za-z0-9_]+)\}/g)].map((match) => match[1]).sort()
}

test('knowledge folder terminology exists in every supported locale', () => {
  for (const [localeName, knowledgeBase] of Object.entries(locales)) {
    const missing = knowledgeFolderKeys.filter((key) => {
      const value = (knowledgeBase as Partial<KnowledgeBaseLocale>)[key]
      return typeof value !== 'string' || value.trim() === ''
    })

    assert.deepEqual(missing, [], `${localeName} is missing folder terminology keys`)
  }
})

test('knowledge folder terminology preserves placeholders across locales', () => {
  for (const key of knowledgeFolderKeys) {
    const expected = placeholders(enUS.knowledgeBase[key])
    for (const [localeName, knowledgeBase] of Object.entries(locales)) {
      assert.deepEqual(
        placeholders(knowledgeBase[key]),
        expected,
        `${localeName}.knowledgeBase.${key} has different placeholders`,
      )
    }
  }
})
