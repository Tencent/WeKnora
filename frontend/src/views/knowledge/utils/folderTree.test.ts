import assert from 'node:assert/strict'
import test from 'node:test'

import type { Folder } from '@/types/folder'
import {
  buildFolderBreadcrumb,
  buildFolderMoveDisabledIds,
  buildFolderTree,
  buildKnowledgeDisplayPaths,
  collectFolderSubtreeIds,
  flattenVisibleFolderTree,
} from './folderTree.ts'

const folder = (id: string, parent_id: string | null, name = id): Folder => ({
  id,
  parent_id,
  knowledge_base_id: 'kb-1',
  name,
  created_at: '',
  updated_at: '',
})

test('builds ordered hierarchy, breadcrumbs, and visible orphan roots', () => {
  const input = [
    folder('root', null, 'Root'),
    folder('child-a', 'root', 'A'),
    folder('grandchild', 'child-a', 'Grandchild'),
    folder('child-b', 'root', 'B'),
    folder('orphan', 'missing', 'Orphan'),
  ]
  const tree = buildFolderTree(input)
  const rows = flattenVisibleFolderTree(tree.roots, new Set(input.map(item => item.id)))
  assert.deepEqual(rows.map(row => [row.folder.id, row.depth]), [
    ['root', 0], ['child-a', 1], ['grandchild', 2], ['child-b', 1], ['orphan', 0],
  ])
  assert.deepEqual(buildFolderBreadcrumb('grandchild', tree.byId).map(item => item.name), [
    'Root', 'A', 'Grandchild',
  ])
  assert.deepEqual([...tree.orphanIds], ['orphan'])
})

test('cycles and a representative deep tree terminate without duplicates', () => {
  const cyclic = buildFolderTree([folder('a', 'b'), folder('b', 'a'), folder('child', 'a')])
  const cycleRows = flattenVisibleFolderTree(cyclic.roots, new Set(['a', 'b', 'child']))
  assert.deepEqual(new Set(cyclic.cyclicIds), new Set(['a', 'b']))
  assert.deepEqual(new Set(cycleRows.map(row => row.folder.id)), new Set(['a', 'b', 'child']))

  const deep = Array.from({ length: 512 }, (_, index) =>
    folder(`deep-${index}`, index ? `deep-${index - 1}` : null))
  const tree = buildFolderTree(deep)
  assert.equal(flattenVisibleFolderTree(tree.roots, new Set(deep.map(item => item.id))).length, 512)
  assert.equal(buildFolderBreadcrumb('deep-511', tree.byId).length, 512)
})

test('subtree and move guards include descendants but exclude unrelated branches', () => {
  const folders = [
    folder('root', null), folder('moving', 'root'), folder('child', 'moving'), folder('other', null),
  ]
  assert.deepEqual(collectFolderSubtreeIds(folders, 'moving'), new Set(['moving', 'child']))
  assert.deepEqual(buildFolderMoveDisabledIds(folders, 'moving', 'root'), new Set([
    'moving', 'child', 'root',
  ]))
})

test('document paths distinguish root and deep files and degrade safely', () => {
  const tree = buildFolderTree([
    folder('project', null, 'Project'),
    folder('requirements', 'project', 'Requirements'),
    folder('orphan', 'missing', 'Orphan'),
  ])
  const paths = buildKnowledgeDisplayPaths([
    { id: 'root', folder_id: null, file_name: 'root.pdf' },
    { id: 'deep', folder_id: 'requirements', file_name: 'design.pdf' },
    { id: 'missing', folder_id: 'not-loaded', file_name: 'missing.pdf' },
  ], tree.byId, 'Root')
  assert.equal(paths.get('root'), 'Root / root.pdf')
  assert.equal(paths.get('deep'), 'Project / Requirements / design.pdf')
  assert.equal(paths.get('missing'), 'missing.pdf')
})
