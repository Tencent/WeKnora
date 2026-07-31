import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

import type { FolderScopeSelection } from '../types/knowledgeScope.ts'
import {
  buildKnowledgeScopeDisplayState,
  confirmKnowledgeScopeDraft,
  createKnowledgeScopeDraft,
  getLastVisibleKnowledgeScopeItem,
  getUnresolvedKnowledgeScopeFileIds,
  removeFolderScopeSelection,
} from '../utils/knowledgeScopeSelection.ts'

const selectorSource = readFileSync(
  new URL('./KnowledgeScopeSelector.vue', import.meta.url),
  'utf8',
)
const mentionSelectorSource = readFileSync(
  new URL('./MentionSelector.vue', import.meta.url),
  'utf8',
)
const inputFieldSource = readFileSync(
  new URL('./Input-field.vue', import.meta.url),
  'utf8',
)
const localeSources = [
  'zh-CN.ts',
  'en-US.ts',
  'ko-KR.ts',
  'ru-RU.ts',
].map(fileName => ({
  fileName,
  source: readFileSync(
    new URL(`../i18n/locales/${fileName}`, import.meta.url),
    'utf8',
  ),
}))

function folder(
  knowledgeBaseId: string,
  folderId: string,
  ancestorFolderIds: string[] = [],
  folderPath: string[] = [folderId],
): FolderScopeSelection {
  return {
    knowledgeBaseId,
    knowledgeBaseName: `KB ${knowledgeBaseId}`,
    folderId,
    folderName: folderPath[folderPath.length - 1] || folderId,
    folderPath,
    ancestorFolderIds,
    includeDescendants: true,
  }
}

test('creates an isolated draft so cancel leaves committed selections unchanged', () => {
  const committedKnowledgeBaseIds = ['kb-whole']
  const committedFolders = [
    folder('kb-folder', 'folder-a', [], ['Projects', 'API']),
  ]
  const draft = createKnowledgeScopeDraft(
    committedKnowledgeBaseIds,
    committedFolders,
  )

  draft.knowledgeBaseIds.push('kb-draft-only')
  draft.folders[0].folderPath.push('Draft only')

  assert.deepEqual(committedKnowledgeBaseIds, ['kb-whole'])
  assert.deepEqual(committedFolders[0].folderPath, ['Projects', 'API'])
})

test('confirm returns a normalized copy for whole-KB, parent, and cross-KB selections', () => {
  const draft = createKnowledgeScopeDraft([], [
    folder('kb-a', 'child-a', ['parent-a']),
    folder('kb-a', 'parent-a'),
    folder('kb-a', 'sibling-a', ['common-parent']),
    folder('kb-b', 'child-a', ['parent-b']),
  ])
  draft.knowledgeBaseIds.push('kb-b')

  const confirmed = confirmKnowledgeScopeDraft(draft)

  assert.deepEqual(confirmed.knowledgeBaseIds, ['kb-b'])
  assert.deepEqual(
    confirmed.folders.map(item => [item.knowledgeBaseId, item.folderId]),
    [
      ['kb-a', 'parent-a'],
      ['kb-a', 'sibling-a'],
    ],
  )
  assert.ok(confirmed.folders.every(item => item.includeDescendants))
  assert.notEqual(confirmed.folders, draft.folders)
})

test('combines folder chips with existing KB, tag, and file items without double counting', () => {
  const selectedItems = [
    { id: 'kb-whole', type: 'kb' },
    { id: 'tag-a', type: 'tag' },
    { id: 'file-a', type: 'file' },
  ]
  const duplicateFolder = folder(
    'kb-folder',
    'folder-a',
    [],
    ['Projects', 'API'],
  )

  const display = buildKnowledgeScopeDisplayState(
    selectedItems,
    [duplicateFolder, { ...duplicateFolder }],
  )

  assert.equal(display.count, 4)
  assert.equal(display.items.length, 4)
  assert.equal(display.folderChips.length, 1)
  assert.deepEqual(display.items.slice(1), selectedItems)
  assert.deepEqual(display.folderChips[0], {
    kind: 'folder-scope',
    key: 'kb-folder:folder-a',
    label: 'KB kb-folder / Projects / API',
    title: 'KB kb-folder / Projects / API',
    scope: duplicateFolder,
  })
})

test('removes one folder by composite identity without affecting the same ID in another KB', () => {
  const remaining = removeFolderScopeSelection(
    [
      folder('kb-a', 'shared-folder-id'),
      folder('kb-b', 'shared-folder-id'),
      folder('kb-a', 'sibling'),
    ],
    'kb-a',
    'shared-folder-id',
  )

  assert.deepEqual(
    remaining.map(item => [item.knowledgeBaseId, item.folderId]),
    [
      ['kb-b', 'shared-folder-id'],
      ['kb-a', 'sibling'],
    ],
  )
})

test('requests file ownership hydration only when folder and file scopes overlap', () => {
  const folders = [folder('kb-a', 'folder-a')]

  assert.deepEqual(
    getUnresolvedKnowledgeScopeFileIds([], ['file-a'], {}),
    [],
  )
  assert.deepEqual(
    getUnresolvedKnowledgeScopeFileIds(
      [{ ...folder('kb-a', 'folder-a'), folderId: '   ' }],
      ['file-a'],
      {},
    ),
    [],
  )
  assert.deepEqual(
    getUnresolvedKnowledgeScopeFileIds(folders, [], {}),
    [],
  )
  assert.deepEqual(
    getUnresolvedKnowledgeScopeFileIds(
      folders,
      ['file-a', 'file-b'],
      { 'file-a': 'kb-a', 'file-b': 'kb-b' },
    ),
    [],
  )
  assert.deepEqual(
    getUnresolvedKnowledgeScopeFileIds(
      folders,
      [' file-a ', 'file-b', 'file-b', 'file-c'],
      { 'file-a': 'kb-a', 'file-b': '', 'file-c': '   ' },
    ),
    ['file-b', 'file-c'],
  )
})

test('finds the last visible chip using the component render order', () => {
  const folders = [{ key: 'folder-a' }, { key: 'folder-b' }]
  const items = [{ id: 'file-a' }, { id: 'tag-a' }]

  assert.deepEqual(
    getLastVisibleKnowledgeScopeItem(folders, items),
    { kind: 'item', item: items[1] },
  )
  assert.deepEqual(
    getLastVisibleKnowledgeScopeItem(folders, []),
    { kind: 'folder', item: folders[1] },
  )
  assert.equal(getLastVisibleKnowledgeScopeItem([], []), null)
})

test('keeps the folder selector inside the existing knowledge-base mention group', () => {
  assert.match(mentionSelectorSource, /\{\s*type:\s*'kb'/)
  assert.match(mentionSelectorSource, /\{\s*type:\s*'tag'/)
  assert.match(mentionSelectorSource, /\{\s*type:\s*'file'/)
  assert.doesNotMatch(mentionSelectorSource, /\{\s*type:\s*'folder'/)
  assert.match(
    mentionSelectorSource,
    /<KnowledgeScopeSelector[\s\S]*?currentGroupType === 'kb'/,
  )
})

test('budgets the expanded popup from the visible view instead of the permission flag', () => {
  assert.match(
    mentionSelectorSource,
    /const isKnowledgeScopeView = computed\(\(\) => \([\s\S]*currentGroupType\.value === 'kb'[\s\S]*props\.enableFolderScope/,
  )
  assert.match(
    mentionSelectorSource,
    /emit\('viewModeChange', active\)/,
  )
  assert.match(
    inputFieldSource,
    /isKnowledgeScopeMentionView\.value\s*\?\s*\{ width: 420, height: 480 \}\s*:\s*\{ width: 300, height: 320 \}/,
  )
  assert.doesNotMatch(
    inputFieldSource,
    /isFolderScopeSelectionEnabled\.value\s*\?\s*\{ width: 420, height: 480 \}/,
  )
})

test('keeps only minimal component guards for lazy loading and native keyboard controls', () => {
  assert.match(selectorSource, /createKnowledgeScopeDraft\(/)
  assert.match(selectorSource, /confirmKnowledgeScopeDraft\(/)
  assert.match(
    selectorSource,
    /listKnowledgeFolders\(knowledgeBaseId,\s*\{[\s\S]*?parent_id:\s*parentId/,
  )
  assert.match(selectorSource, /let requestSequence = 0/)
  assert.match(selectorSource, /state\.requestId !== requestId/)
  assert.doesNotMatch(selectorSource, /searchQuery|searchPlaceholder/)
  assert.match(selectorSource, /<button[\s\S]*?toggleKnowledgeBaseExpanded/)
  assert.match(selectorSource, /<t-checkbox[\s\S]*?toggleKnowledgeBase/)
  assert.match(selectorSource, /emit\('cancel'\)/)
  assert.match(selectorSource, /emit\('confirm'/)
  assert.match(
    mentionSelectorSource,
    /knowledgeScopeSelectorRef\.value\?\.focusFirstControl\(\)/,
  )
})

test('exposes real folder rows as accessible tree items with neutral icons', () => {
  assert.match(
    selectorSource,
    /:role="row\.kind === 'folder' \? 'treeitem' : undefined"/,
  )
  assert.match(
    selectorSource,
    /:aria-level="row\.kind === 'folder' \? row\.depth : undefined"/,
  )
  assert.match(
    selectorSource,
    /:aria-expanded="\(\s*row\.kind === 'folder' && row\.folder\.has_children\s*\?\s*expandedIds\.has\(expandedKey\(knowledgeBase\.id, row\.folder\.id\)\)\s*:\s*undefined\s*\)"/,
  )

  const folderIconSource = selectorSource.match(
    /<t-icon\s+:name="expandedIds\.has\(expandedKey\(knowledgeBase\.id, row\.folder\.id\)\)\s*\?\s*'folder-open'\s*:\s*'folder'"\s+class="knowledge-scope-selector__icon"\s*\/>/,
  )
  assert.ok(folderIconSource)
  assert.doesNotMatch(folderIconSource[0], /warning/)
  assert.match(
    selectorSource,
    /\.knowledge-scope-selector__icon\s*\{\s*flex:\s*0 0 auto;\s*color:\s*var\(--td-text-color-secondary\);/,
  )
})

test('keeps folder chips out of the request mentioned_items projection', () => {
  assert.match(inputFieldSource, /buildFolderScopeChips\(/)
  assert.match(inputFieldSource, /buildKnowledgeScopeDisplayState\(/)
  assert.match(inputFieldSource, /removeFolderScopeSelection\(/)
  assert.match(
    inputFieldSource,
    /const mentionedItems:[\s\S]*?allSelectedItems\.value\.map/,
  )
  assert.doesNotMatch(
    inputFieldSource,
    /const mentionedItems:[\s\S]*?selectedFolderChips\.value\.map/,
  )
})

test('hydrates unresolved file ownership once before sending a folder scope', () => {
  const hydrationGuardSource = inputFieldSource.match(
    /const ensureFolderScopeFileOwnership = async \(\): Promise<boolean> => \{[\s\S]*?\n\};/,
  )
  assert.ok(hydrationGuardSource)
  assert.match(
    inputFieldSource,
    /let folderScopeFileHydrationPromise:\s*Promise<void>\s*\|\s*null\s*=\s*null/,
  )
  assert.match(
    hydrationGuardSource[0],
    /if \(!folderScopeFileHydrationPromise\) \{\s*folderScopeFileHydrationPromise = loadFiles\(unresolvedFileIds\);\s*\}/,
  )
  assert.match(
    hydrationGuardSource[0],
    /const hydrationPromise = folderScopeFileHydrationPromise;\s*try \{\s*await hydrationPromise;/,
  )
  assert.match(
    hydrationGuardSource[0],
    /const unresolvedFileIds = getUnresolvedKnowledgeScopeFileIds\(\s*isFolderScopeSelectionEnabled\.value\s*\?\s*selectedFolderScopes\.value\s*:\s*\[\],\s*selectedFileIds\.value,\s*effectiveFileKbMap\.value,\s*\)/,
  )
  assert.match(
    hydrationGuardSource[0],
    /finally \{\s*if \(folderScopeFileHydrationPromise === hydrationPromise\) \{\s*folderScopeFileHydrationPromise = null;/,
  )
  assert.match(
    hydrationGuardSource[0],
    /const stillUnresolved = getUnresolvedKnowledgeScopeFileIds\(\s*isFolderScopeSelectionEnabled\.value\s*\?\s*selectedFolderScopes\.value\s*:\s*\[\],\s*selectedFileIds\.value,\s*effectiveFileKbMap\.value,\s*\);[\s\S]*?if \(stillUnresolved\.length > 0\) \{[\s\S]*?return false;\s*\}\s*return true;/,
  )
  assert.match(
    inputFieldSource,
    /if\s*\(!await ensureFolderScopeFileOwnership\(\)\)\s*\{\s*return;/,
  )
  assert.match(
    inputFieldSource,
    /MessagePlugin\.error\(t\('knowledgeScope\.fileOwnershipUnavailable'\)\)/,
  )
  assert.match(
    inputFieldSource,
    /const effectiveFileKbMap = computed<Record<string, string>>\(\(\) => \{[\s\S]*?settingsStore\.settings\.selectedFileKbMap[\s\S]*?fileIdToKbId\.value[\s\S]*?return merged;/,
  )
  assert.match(
    inputFieldSource,
    /const kbId = effectiveFileKbMap\.value\[f\.id\];[\s\S]*?kbId: kbId \|\| undefined/,
  )
  assert.match(
    inputFieldSource,
    /const mentionedItems:[\s\S]*?kb_id: item\.kbId/,
  )
})

test('backspace removes the last chip from the visible folder-first order', () => {
  assert.match(
    inputFieldSource,
    /if \(event\.e\.keyCode === 8\)[\s\S]*?textarea\.selectionStart === 0 && textarea\.selectionEnd === 0 && query\.value === ''[\s\S]*?getLastVisibleKnowledgeScopeItem\(\s*selectedFolderChips\.value,\s*allSelectedItems\.value,/,
  )
  assert.match(
    inputFieldSource,
    /if \(lastVisibleItem\) \{\s*event\.e\.preventDefault\(\);[\s\S]*?lastVisibleItem\.kind === 'folder'[\s\S]*?removeFolderScope\(lastVisibleItem\.item\.scope\)[\s\S]*?removeSelectedItem\(lastVisibleItem\.item\)/,
  )
})

test('defines the file ownership error in every supported locale', () => {
  for (const { fileName, source } of localeSources) {
    const section = source.match(
      /\n\s*knowledgeScope:\s*\{([\s\S]*?)\n\s*\},\n\s*knowledgeBase:/,
    )?.[1]
    assert.ok(section, `${fileName}: missing knowledgeScope section`)
    assert.match(
      section,
      /\bfileOwnershipUnavailable\s*:/,
      `${fileName}: missing fileOwnershipUnavailable`,
    )
  }
})
