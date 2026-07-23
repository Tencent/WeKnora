import assert from 'node:assert/strict'
import test from 'node:test'

import type { Folder } from '../types/folder'
import {
  buildFolderScopes,
  normalizeFolderIDs,
  normalizeSelectedFolderScopes,
  pruneFolderScopes,
  resolveFolderScopeState,
  restoreFolderScopes,
} from './folderScope.ts'
import {
  selectEntireKnowledgeBaseScope,
  toggleSourceFolderScope,
} from './sourceFolderSelection.ts'

function folder(id: string, parentId: string | null, name = id, kbId = 'kb-1'): Folder {
  return {
    id,
    parent_id: parentId,
    knowledge_base_id: kbId,
    name,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
  }
}

test('normalization migrates strings, deduplicates arrays, filters invalid values, and stays stable', () => {
  assert.deepEqual(normalizeFolderIDs([' folder-b ', '', 'folder-a', 'folder-b', null]), [
    'folder-a',
    'folder-b',
  ])
  assert.deepEqual(normalizeSelectedFolderScopes({
    ' kb-1 ': ' folder-a ',
    'kb-2': ['folder-c', 'folder-b', 'folder-c', ''],
    'kb-3': [],
    'kb-4': null,
  }), {
    'kb-1': ['folder-a'],
    'kb-2': ['folder-b', 'folder-c'],
  })
})

test('buildFolderScopes emits one immutable multi-folder scope per selected KB', () => {
  const selectedScopes = {
    'kb-2': ['folder-d'],
    'kb-1': ['folder-b', 'folder-a', 'folder-b'],
    'kb-unselected': ['folder-x'],
  }
  const snapshot = structuredClone(selectedScopes)

  assert.deepEqual(buildFolderScopes(['kb-1', 'kb-1', 'kb-2', 'kb-3'], selectedScopes), [
    { knowledge_base_id: 'kb-1', folder_ids: ['folder-a', 'folder-b'] },
    { knowledge_base_id: 'kb-2', folder_ids: ['folder-d'] },
  ])
  assert.deepEqual(selectedScopes, snapshot)
})

test('whole-KB and empty scopes are omitted and prune keeps only selected KBs', () => {
  assert.deepEqual(buildFolderScopes(['kb-1', 'kb-2'], {
    'kb-1': [],
    'kb-2': undefined,
  }), [])
  assert.deepEqual(pruneFolderScopes({
    'kb-1': ['folder-a'],
    'kb-2': ['folder-c', 'folder-b'],
  }, ['kb-2']), {
    'kb-2': ['folder-b', 'folder-c'],
  })
})

test('session restore accepts new and legacy shapes without losing later folders', () => {
  assert.deepEqual(restoreFolderScopes(undefined, ['kb-1']), {})
  assert.deepEqual(restoreFolderScopes([
    { knowledge_base_id: 'kb-1', folder_id: null },
    { knowledge_base_id: 'kb-1', folder_id: 'folder-a' },
    { knowledge_base_id: 'kb-1', folder_ids: ['folder-c', 'folder-b', 'folder-a'] },
    { knowledge_base_id: 'kb-2', folder_ids: ['folder-d'] },
    { knowledge_base_id: '', folder_id: 'folder-x' },
  ], ['kb-1']), {
    'kb-1': ['folder-a', 'folder-b', 'folder-c'],
  })
})

test('resolved state preserves every folder and identifies only invalid IDs', () => {
  const folders = [
    folder('root', null, 'Products'),
    folder('child', 'root', 'Requirements'),
    folder('sibling', null, 'Finance'),
  ]

  assert.equal(resolveFolderScopeState(undefined, folders, false).status, 'entire-kb')
  assert.equal(resolveFolderScopeState(['child'], undefined, true).status, 'loading')
  assert.equal(resolveFolderScopeState(['child'], undefined, false, new Error('boom')).status, 'load-error')

  const valid = resolveFolderScopeState(['sibling', 'child'], folders, false)
  assert.equal(valid.status, 'valid')
  if (valid.status === 'valid') {
    assert.deepEqual(valid.paths, ['Products / Requirements', 'Finance'])
  }

  const invalid = resolveFolderScopeState(['missing', 'child'], folders, false)
  assert.equal(invalid.status, 'invalid')
  if (invalid.status === 'invalid') {
    assert.deepEqual(invalid.invalidFolderIDs, ['missing'])
    assert.deepEqual(invalid.folders.map(item => item.id), ['child'])
  }
})

test('source selection toggles multiple folders and whole-KB clears only that scope', () => {
  const selectedKBs = new Set<string>()
  const scopes: Record<string, string[]> = {}
  const store = {
    addKnowledgeBase: (kbID: string) => { selectedKBs.add(kbID) },
    getFolderScopes: (kbID: string) => scopes[kbID] || [],
    toggleFolderScope: (kbID: string, folderID: string) => {
      const current = scopes[kbID] || []
      scopes[kbID] = current.includes(folderID)
        ? current.filter(id => id !== folderID)
        : [...current, folderID].sort()
      if (scopes[kbID].length === 0) delete scopes[kbID]
    },
    clearFolderScopes: (kbID: string) => { delete scopes[kbID] },
  }

  assert.equal(toggleSourceFolderScope(store, 'kb-1', 'folder-b'), 'updated')
  assert.equal(toggleSourceFolderScope(store, 'kb-1', 'folder-a'), 'updated')
  assert.deepEqual(scopes, { 'kb-1': ['folder-a', 'folder-b'] })
  assert.equal(toggleSourceFolderScope(store, 'kb-1', 'folder-a'), 'updated')
  assert.deepEqual(scopes, { 'kb-1': ['folder-b'] })
  assert.equal(selectEntireKnowledgeBaseScope(store, 'kb-1'), 'updated')
  assert.deepEqual(scopes, {})
  assert.deepEqual([...selectedKBs], ['kb-1'])
})
