import assert from 'node:assert/strict'
import test from 'node:test'

import { buildKnowledgeListSearchParams } from '../../../api/knowledge-base/query.ts'
import type { Folder } from '@/types/folder'
import { isDragTargetAllowed, parseActiveDragPayload, serializeActiveDrag } from './folderDrag.ts'
import { parseFolderRouteQuery, withFolderRouteQuery } from './folderRoute.ts'
import { resolveBatchKnowledgeFolderSources } from './knowledgeBatchMove.ts'

const ids = {
  root: '00000000-0000-4000-8000-000000000001',
  child: '00000000-0000-4000-8000-000000000002',
  doc: '00000000-0000-4000-8000-000000000003',
}

const folder = (id: string, parent_id: string | null): Folder => ({
  id,
  parent_id,
  knowledge_base_id: 'kb-1',
  name: id,
  created_at: '',
  updated_at: '',
})

test('folder and knowledge moves keep their safety and batch source boundaries', () => {
  const folders = [folder(ids.root, null), folder(ids.child, ids.root)]
  const drag = { type: 'folder' as const, folderId: ids.root, sourceParentId: null }
  assert.deepEqual(parseActiveDragPayload(serializeActiveDrag(drag)), drag)
  assert.equal(isDragTargetAllowed(drag, ids.child, folders), false)
  assert.equal(isDragTargetAllowed({
    type: 'knowledge', knowledgeId: ids.doc, sourceFolderId: ids.child,
  }, ids.child, folders), false)

  assert.deepEqual(resolveBatchKnowledgeFolderSources(
    ['root-doc', 'nested-doc', 'nested-doc'],
    [{ id: 'root-doc', folder_id: null }, { id: 'nested-doc', folder_id: ids.child }],
  ), {
    ids: ['root-doc', 'nested-doc'],
    disabledFolderIds: new Set([ids.child]),
    rootDisabled: true,
    unresolvedIds: [],
  })
})

test('folder URL and list requests preserve root versus folder semantics', () => {
  assert.deepEqual(parseFolderRouteQuery(undefined), { folderId: null, invalid: false })
  assert.deepEqual(parseFolderRouteQuery(ids.child), { folderId: ids.child, invalid: false })
  assert.deepEqual(withFolderRouteQuery({ knowledge_id: 'doc', tab: 'files' }, ids.child), {
    folder_id: ids.child,
    tab: 'files',
  })

  const root = buildKnowledgeListSearchParams({ page: 1, page_size: 10, folder_id: null })
  const scoped = buildKnowledgeListSearchParams({ page: 1, page_size: 10, folder_id: ids.child })
  assert.equal(root.has('folder_id'), true)
  assert.equal(root.get('folder_id'), '')
  assert.equal(scoped.get('folder_id'), ids.child)
})
