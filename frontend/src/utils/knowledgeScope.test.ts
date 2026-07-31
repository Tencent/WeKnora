import assert from 'node:assert/strict'
import test from 'node:test'

import type {
  FolderScopeSelection,
  KnowledgeScopeRequest,
} from '../types/knowledgeScope.ts'
import {
  buildKnowledgeScopeProjection,
  buildKnowledgeScopeRequest,
  cloneKnowledgeScopeRequest,
  folderSelectionsFromRequest,
  knowledgeBaseSelectionsFromRequest,
  normalizeFolderScopeSelections,
} from './knowledgeScope.ts'

function folderSelection(
  knowledgeBaseId: string,
  folderId: string,
  ancestorFolderIds: string[] = [],
): FolderScopeSelection {
  return {
    knowledgeBaseId,
    knowledgeBaseName: `Knowledge base ${knowledgeBaseId}`,
    folderId,
    folderName: `Folder ${folderId}`,
    folderPath: [`Folder ${folderId}`],
    ancestorFolderIds,
    includeDescendants: true,
  }
}

test('clones knowledge scope without conflating missing and empty folder scopes', () => {
  const missing: KnowledgeScopeRequest = {
    knowledge_base_ids: ['kb-a'],
  }
  const enabledEmpty: KnowledgeScopeRequest = {
    knowledge_base_ids: ['kb-a'],
    folder_scopes: [],
  }
  const explicitlyUndefined: KnowledgeScopeRequest = {
    knowledge_base_ids: ['kb-a'],
    folder_scopes: undefined,
  }

  const missingClone = cloneKnowledgeScopeRequest(missing)
  const enabledEmptyClone = cloneKnowledgeScopeRequest(enabledEmpty)
  const explicitlyUndefinedClone =
    cloneKnowledgeScopeRequest(explicitlyUndefined)

  assert.deepEqual(missingClone, missing)
  assert.equal(Object.hasOwn(missingClone, 'folder_scopes'), false)
  assert.notEqual(missingClone.knowledge_base_ids, missing.knowledge_base_ids)

  assert.deepEqual(enabledEmptyClone, enabledEmpty)
  assert.equal(Object.hasOwn(enabledEmptyClone, 'folder_scopes'), true)
  assert.notEqual(enabledEmptyClone.folder_scopes, enabledEmpty.folder_scopes)

  assert.equal(
    Object.hasOwn(explicitlyUndefinedClone, 'folder_scopes'),
    true,
  )
  assert.equal(explicitlyUndefinedClone.folder_scopes, undefined)
})

test('deep-clones mixed tag, file, root, and non-root scope arrays', () => {
  const request: KnowledgeScopeRequest = {
    knowledge_base_ids: ['kb-whole'],
    knowledge_ids: ['file-a'],
    tag_scopes: [{
      knowledge_base_id: 'kb-tag',
      tag_ids: ['tag-a'],
    }],
    folder_scopes: [
      {
        knowledge_base_id: 'kb-whole',
        folder_ids: [''],
        include_descendants: true,
      },
      {
        knowledge_base_id: 'kb-folder',
        folder_ids: ['folder-a'],
        include_descendants: false,
      },
      {
        knowledge_base_id: 'kb-direct',
        folder_ids: ['folder-direct'],
      },
    ],
  }

  const cloned = cloneKnowledgeScopeRequest(request)

  assert.deepEqual(cloned, request)
  assert.notEqual(cloned.knowledge_base_ids, request.knowledge_base_ids)
  assert.notEqual(cloned.knowledge_ids, request.knowledge_ids)
  assert.notEqual(cloned.tag_scopes, request.tag_scopes)
  assert.notEqual(cloned.tag_scopes?.[0], request.tag_scopes?.[0])
  assert.notEqual(cloned.tag_scopes?.[0].tag_ids, request.tag_scopes?.[0].tag_ids)
  assert.notEqual(cloned.folder_scopes, request.folder_scopes)
  assert.notEqual(cloned.folder_scopes?.[0], request.folder_scopes?.[0])
  assert.notEqual(
    cloned.folder_scopes?.[0].folder_ids,
    request.folder_scopes?.[0].folder_ids,
  )
  assert.equal(cloned.folder_scopes?.[0].include_descendants, true)
  assert.equal(cloned.folder_scopes?.[1].include_descendants, false)
  assert.equal(
    Object.hasOwn(cloned.folder_scopes?.[2] || {}, 'include_descendants'),
    false,
  )

  cloned.knowledge_base_ids?.push('kb-added')
  cloned.knowledge_ids?.push('file-added')
  cloned.tag_scopes?.[0].tag_ids.push('tag-added')
  cloned.folder_scopes?.[0].folder_ids.push('folder-added')

  assert.deepEqual(request.knowledge_base_ids, ['kb-whole'])
  assert.deepEqual(request.knowledge_ids, ['file-a'])
  assert.deepEqual(request.tag_scopes?.[0].tag_ids, ['tag-a'])
  assert.deepEqual(request.folder_scopes?.[0].folder_ids, [''])
})

test('a selected parent covers its selected descendants by stable folder IDs', () => {
  const parent = folderSelection('kb-a', 'folder-parent')
  const child = folderSelection('kb-a', 'folder-child', ['folder-parent'])

  assert.deepEqual(normalizeFolderScopeSelections([child, parent]), [parent])
})

test('a whole knowledge base covers folder selections from the same knowledge base', () => {
  const folder = folderSelection('kb-a', 'folder-a')

  assert.deepEqual(normalizeFolderScopeSelections([folder], ['kb-a']), [])
})

test('sibling folders and folders from different knowledge bases remain selected', () => {
  const left = folderSelection('kb-a', 'folder-left', ['folder-parent'])
  const right = folderSelection('kb-a', 'folder-right', ['folder-parent'])
  const otherKnowledgeBase = folderSelection('kb-b', 'folder-left', ['folder-parent'])

  assert.deepEqual(
    normalizeFolderScopeSelections([left, right, otherKnowledgeBase]),
    [left, right, otherKnowledgeBase],
  )
})

test('removing a normalized parent does not restore a previously covered child', () => {
  const parent = folderSelection('kb-a', 'folder-parent')
  const child = folderSelection('kb-a', 'folder-child', ['folder-parent'])
  const normalized = normalizeFolderScopeSelections([parent, child])

  assert.deepEqual(
    normalizeFolderScopeSelections(
      normalized.filter(selection => selection.folderId !== parent.folderId),
    ),
    [],
  )
})

test('projects canonical folder scopes separately from the legacy KB universe', () => {
  const projection = buildKnowledgeScopeProjection({
    knowledgeBaseIds: ['kb-whole', 'kb-whole'],
    knowledgeIds: ['knowledge-b', '', 'knowledge-a', 'knowledge-a'],
    knowledgeSelections: [
      { id: 'knowledge-a', kbId: 'kb-folder-a' },
      { id: 'knowledge-b', kbId: 'kb-folder-b' },
    ],
    tags: [
      { id: 'tag-b', kbId: 'kb-folder-a' },
      { id: 'tag-a', kbId: 'kb-folder-a' },
      { id: '', kbId: 'kb-folder-a' },
    ],
    folderSelections: [
      folderSelection('kb-folder-a', 'folder-b'),
      folderSelection('kb-folder-a', 'folder-a'),
      folderSelection('kb-folder-a', 'folder-a'),
      folderSelection('kb-folder-b', 'folder-c'),
      folderSelection('kb-whole', 'covered-by-whole-kb'),
      folderSelection('kb-folder-a', '   '),
      folderSelection('   ', 'folder-without-kb'),
    ],
  })

  assert.deepEqual(projection, {
    knowledge_base_ids: ['kb-folder-a', 'kb-folder-b', 'kb-whole'],
    knowledge_scope: {
      knowledge_base_ids: ['kb-whole'],
      knowledge_ids: ['knowledge-a', 'knowledge-b'],
      tag_scopes: [
        {
          knowledge_base_id: 'kb-folder-a',
          tag_ids: ['tag-a', 'tag-b'],
        },
      ],
      folder_scopes: [
        {
          knowledge_base_id: 'kb-folder-a',
          folder_ids: ['folder-a', 'folder-b'],
          include_descendants: true,
        },
        {
          knowledge_base_id: 'kb-folder-b',
          folder_ids: ['folder-c'],
          include_descendants: true,
        },
        {
          knowledge_base_id: 'kb-whole',
          folder_ids: [''],
          include_descendants: true,
        },
      ],
    },
  })
})

test('omits knowledge_scope when no valid folder selection remains', () => {
  const input = {
    knowledgeBaseIds: ['kb-whole'],
    knowledgeIds: ['knowledge-a'],
    tags: [{ id: 'tag-a', kbId: 'kb-whole' }],
    folderSelections: [
      folderSelection('kb-whole', 'covered-by-whole-kb'),
      folderSelection('kb-other', ''),
    ],
  }
  const projection = buildKnowledgeScopeProjection(input)

  assert.deepEqual(projection, {
    knowledge_base_ids: ['kb-whole'],
  })
  assert.equal(Object.hasOwn(projection, 'knowledge_scope'), false)
  assert.equal(buildKnowledgeScopeRequest(input), undefined)
})

test('keeps existing file and tag scope when folders are present', () => {
  const projection = buildKnowledgeScopeProjection({
    knowledgeBaseIds: [],
    knowledgeIds: ['file-b', 'file-a'],
    knowledgeSelections: [
      { id: 'file-b', kbId: 'kb-a' },
      { id: 'file-a', kbId: 'kb-a' },
    ],
    tags: [
      { id: 'tag-b', kbId: 'kb-a' },
      { id: 'tag-a', kbId: 'kb-a' },
    ],
    folderSelections: [folderSelection('kb-a', 'folder-a')],
  })

  assert.deepEqual(projection.knowledge_scope, {
    knowledge_ids: ['file-a', 'file-b'],
    tag_scopes: [{
      knowledge_base_id: 'kb-a',
      tag_ids: ['tag-a', 'tag-b'],
    }],
    folder_scopes: [{
      knowledge_base_id: 'kb-a',
      folder_ids: ['folder-a'],
      include_descendants: true,
    }],
  })
})

test('A: records only explicit whole-KB A beside folder B/X', () => {
  const projection = buildKnowledgeScopeProjection({
    knowledgeBaseIds: ['kb-a'],
    knowledgeIds: [],
    tags: [],
    folderSelections: [folderSelection('kb-b', 'folder-x')],
  })

  assert.deepEqual(projection, {
    knowledge_base_ids: ['kb-a', 'kb-b'],
    knowledge_scope: {
      knowledge_base_ids: ['kb-a'],
      folder_scopes: [
        {
          knowledge_base_id: 'kb-a',
          folder_ids: [''],
          include_descendants: true,
        },
        {
          knowledge_base_id: 'kb-b',
          folder_ids: ['folder-x'],
          include_descendants: true,
        },
      ],
    },
  })
})

test('B: keeps file KB-B out of canonical whole-KB IDs while retaining its root', () => {
  const projection = buildKnowledgeScopeProjection({
    knowledgeBaseIds: [],
    knowledgeIds: ['file-b'],
    knowledgeSelections: [{ id: 'file-b', kbId: 'kb-b' }],
    tags: [],
    folderSelections: [folderSelection('kb-a', 'folder-x')],
  })

  assert.deepEqual(projection, {
    knowledge_base_ids: ['kb-a', 'kb-b'],
    knowledge_scope: {
      knowledge_ids: ['file-b'],
      folder_scopes: [
        {
          knowledge_base_id: 'kb-a',
          folder_ids: ['folder-x'],
          include_descendants: true,
        },
        {
          knowledge_base_id: 'kb-b',
          folder_ids: [''],
          include_descendants: true,
        },
      ],
    },
  })
  assert.equal(
    Object.hasOwn(projection.knowledge_scope || {}, 'knowledge_base_ids'),
    false,
  )
})

test('C: keeps tag KB-B out of canonical whole-KB IDs while retaining its root', () => {
  const projection = buildKnowledgeScopeProjection({
    knowledgeBaseIds: [],
    knowledgeIds: [],
    tags: [{ id: 'tag-b', kbId: 'kb-b' }],
    folderSelections: [folderSelection('kb-a', 'folder-x')],
  })

  assert.deepEqual(projection, {
    knowledge_base_ids: ['kb-a', 'kb-b'],
    knowledge_scope: {
      tag_scopes: [{
        knowledge_base_id: 'kb-b',
        tag_ids: ['tag-b'],
      }],
      folder_scopes: [
        {
          knowledge_base_id: 'kb-a',
          folder_ids: ['folder-x'],
          include_descendants: true,
        },
        {
          knowledge_base_id: 'kb-b',
          folder_ids: [''],
          include_descendants: true,
        },
      ],
    },
  })
  assert.equal(
    Object.hasOwn(projection.knowledge_scope || {}, 'knowledge_base_ids'),
    false,
  )
})

test('does not replace a real folder with root for same-KB file and tag targets', () => {
  const projection = buildKnowledgeScopeProjection({
    knowledgeBaseIds: [],
    knowledgeIds: ['file-a'],
    knowledgeSelections: [{ id: 'file-a', kbId: 'kb-a' }],
    tags: [{ id: 'tag-a', kbId: 'kb-a' }],
    folderSelections: [folderSelection('kb-a', 'folder-a')],
  })

  assert.deepEqual(projection.knowledge_scope?.folder_scopes, [{
    knowledge_base_id: 'kb-a',
    folder_ids: ['folder-a'],
    include_descendants: true,
  }])
})

test('D: keeps a folder-only scope free of canonical whole-KB IDs', () => {
  const projection = buildKnowledgeScopeProjection({
    knowledgeBaseIds: [],
    knowledgeIds: [],
    tags: [],
    folderSelections: [folderSelection('kb-a', 'folder-x')],
  })

  assert.deepEqual(projection, {
    knowledge_base_ids: ['kb-a'],
    knowledge_scope: {
      folder_scopes: [{
        knowledge_base_id: 'kb-a',
        folder_ids: ['folder-x'],
        include_descendants: true,
      }],
    },
  })
  assert.equal(
    Object.hasOwn(projection.knowledge_scope || {}, 'knowledge_base_ids'),
    false,
  )
})

test('restores canonical folder scopes as safe ID-based selections', () => {
  const restored = folderSelectionsFromRequest({
    folder_scopes: [
      {
        knowledge_base_id: 'kb-a',
        folder_ids: ['folder-b', '', 'folder-a', 'folder-a'],
        include_descendants: true,
      },
      {
        knowledge_base_id: 'kb-b',
        folder_ids: ['folder-a'],
        include_descendants: true,
      },
    ],
  })

  assert.deepEqual(
    restored.map(item => ({
      knowledgeBaseId: item.knowledgeBaseId,
      knowledgeBaseName: item.knowledgeBaseName,
      folderId: item.folderId,
      folderPath: item.folderPath,
      ancestorFolderIds: item.ancestorFolderIds,
      includeDescendants: item.includeDescendants,
    })),
    [
      {
        knowledgeBaseId: 'kb-a',
        knowledgeBaseName: 'kb-a',
        folderId: 'folder-b',
        folderPath: ['folder-b'],
        ancestorFolderIds: [],
        includeDescendants: true,
      },
      {
        knowledgeBaseId: 'kb-a',
        knowledgeBaseName: 'kb-a',
        folderId: 'folder-a',
        folderPath: ['folder-a'],
        ancestorFolderIds: [],
        includeDescendants: true,
      },
      {
        knowledgeBaseId: 'kb-b',
        knowledgeBaseName: 'kb-b',
        folderId: 'folder-a',
        folderPath: ['folder-a'],
        ancestorFolderIds: [],
        includeDescendants: true,
      },
    ],
  )
})

test('E: restores canonical whole-KB IDs without promoting compatibility roots', () => {
  const request = {
    knowledge_base_ids: ['kb-whole'],
    folder_scopes: [
      {
        knowledge_base_id: 'kb-whole',
        folder_ids: [''],
        include_descendants: true,
      },
      {
        knowledge_base_id: 'kb-compat-root',
        folder_ids: [''],
        include_descendants: true,
      },
      {
        knowledge_base_id: 'kb-direct-root',
        folder_ids: [''],
        include_descendants: false,
      },
      {
        knowledge_base_id: 'kb-folder',
        folder_ids: ['folder-a'],
        include_descendants: true,
      },
    ],
  }

  assert.deepEqual(
    knowledgeBaseSelectionsFromRequest(request),
    ['kb-whole'],
  )
  assert.deepEqual(
    folderSelectionsFromRequest(request)
      .map(item => [item.knowledgeBaseId, item.folderId]),
    [['kb-folder', 'folder-a']],
  )
})
