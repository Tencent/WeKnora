import assert from 'node:assert/strict'
import test from 'node:test'
import type { KnowledgeFolder } from '../../types/knowledgeFolder.ts'
import {
  buildFolderIndex,
  buildRenderedSelectionKeys,
  countDirectChildren,
  descendantIds,
  flattenFolders,
  folderPathItems,
  folderPathLabel,
  insertCreatePlaceholder,
  isMoveTargetDisabled,
  searchFolders,
  selectionCapabilities,
  selectionKeys,
  sortDirectFolders,
} from './folderModel.ts'

const folder = (id: string, parent_id: string, name: string, depth: number): KnowledgeFolder => ({
  id, parent_id, name, depth,
  knowledge_base_id: 'kb-1', path: `/${id}`, sort_order: 0,
  knowledge_count: 0, created_at: '', updated_at: '', children: [],
})

const nullableCount = (id: string): KnowledgeFolder => ({
  id, parent_id: '', name: id, depth: 1,
  knowledge_base_id: 'kb-1', path: `/${id}`, sort_order: 0,
  knowledge_count: undefined, created_at: '', updated_at: '', children: [],
})

const tree = [
  { ...folder('a', '', '产品', 1), children: [
    { ...folder('b', 'a', '2026', 2), children: [folder('c', 'b', '合同', 3)] },
  ] },
  folder('d', '', '技术', 1),
]

test('indexes paths and descendants without exposing root sentinel', () => {
  const index = buildFolderIndex(tree)
  assert.equal(index.byId.get('c')?.name, '合同')
  assert.deepEqual(index.pathIds.get('c'), ['a', 'b', 'c'])
  assert.deepEqual(descendantIds(index, 'a'), new Set(['a', 'b', 'c']))
})

test('searches all folders and returns path labels', () => {
  const results = searchFolders(buildFolderIndex(tree), '合同')
  assert.deepEqual(results.map((item) => [item.folder.id, item.pathLabel]), [
    ['c', '根目录 / 产品 / 2026 / 合同'],
  ])
})

test('folderPathItems returns folder objects in root-first order', () => {
  const index = buildFolderIndex(tree)
  assert.deepEqual(folderPathItems(index, 'c').map((item) => item.id), ['a', 'b', 'c'])
  assert.deepEqual(folderPathItems(index, 'a').map((item) => item.id), ['a'])
  assert.deepEqual(folderPathItems(index, 'missing'), [])
})

test('folderPathLabel uses the caller-provided root label instead of hard-coded Chinese', () => {
  const index = buildFolderIndex(tree)
  assert.equal(folderPathLabel(index, 'c', 'Root'), 'Root / 产品 / 2026 / 合同')
  assert.equal(folderPathLabel(index, 'a', 'Root'), 'Root / 产品')
  assert.equal(folderPathLabel(index, 'missing', 'Root'), 'Root')
})

test('searchFolders derives its path labels from the localized root label', () => {
  const results = searchFolders(buildFolderIndex(tree), '合同')
  assert.equal(results[0].pathLabel, '根目录 / 产品 / 2026 / 合同')
})

test('countDirectChildren returns zero for childless and unknown folders', () => {
  const index = buildFolderIndex(tree)
  assert.equal(countDirectChildren(index, 'd'), 0)
  assert.equal(countDirectChildren(index, 'missing'), 0)
  assert.equal(countDirectChildren(index, 'a'), 1)
})

test('nullable knowledge_count folders are indexed like any other folder', () => {
  const index = buildFolderIndex([nullableCount('n1'), folder('a', 'n1', 'child', 2)])
  assert.equal(index.byId.get('n1')?.knowledge_count, undefined)
  assert.deepEqual(folderPathItems(index, 'a').map((item) => item.id), ['n1', 'a'])
})

test('duplicate folder ids keep the first occurrence and ignore later siblings', () => {
  const first = folder('dup', '', 'first', 1)
  const later = { ...folder('dup', '', 'later', 1), name: 'later' }
  const index = buildFolderIndex([first, later])
  assert.equal(index.byId.get('dup')?.name, 'first')
})

test('child-object cycles do not recurse forever', () => {
  const a = folder('a', 'b', 'A', 1) as KnowledgeFolder
  const b = folder('b', 'a', 'B', 2) as KnowledgeFolder
  a.children = [b]
  b.children = [a]
  const index = buildFolderIndex([a])
  assert.ok(index.byId.has('a'))
  assert.ok(index.byId.has('b'))
})

test('parent-cycle path resolution stops and marks the folder invalid', () => {
  // a -> b -> a forms a parent_id cycle that path resolution must terminate on.
  const a = folder('a', 'b', 'A', 1)
  const b = folder('b', 'a', 'B', 2)
  const index = buildFolderIndex([a, b])
  // Both paths must resolve to finite arrays without infinite recursion...
  const pathA = index.pathIds.get('a')
  const pathB = index.pathIds.get('b')
  assert.ok(Array.isArray(pathA) && pathA.length < 5, 'path a must terminate finitely')
  assert.ok(Array.isArray(pathB) && pathB.length < 5, 'path b must terminate finitely')
  // ...and both cycled folders are recorded as invalid paths.
  assert.ok(index.invalidPaths.has('a'), 'a is part of a parent cycle')
  assert.ok(index.invalidPaths.has('b'), 'b is part of a parent cycle')
})

test('missing parent records the folder as an orphan whose path starts at itself', () => {
  const orphan = folder('o', 'ghost', 'Orphan', 1)
  const index = buildFolderIndex([orphan])
  assert.deepEqual(index.pathIds.get('o'), ['o'])
})

test('flattenFolders does not mutate the input tree', () => {
  const original = JSON.parse(JSON.stringify(tree)) as KnowledgeFolder[]
  flattenFolders(tree)
  assert.deepEqual(tree, original)
})

test('buildRenderedSelectionKeys orders folders first by rendered order then documents by loaded order', () => {
  const index = buildFolderIndex(tree)
  const directFolders = index.childrenByParent.get('') || []
  const documents = [
    { id: 'k2', folder_id: '' },
    { id: 'k1', folder_id: '' },
    { id: 'k3', folder_id: 'a' },
  ] as Array<{ id: string; folder_id: string }>
  const keys = buildRenderedSelectionKeys(directFolders, documents)
  // Direct folders (a, d) come before documents, in rendered folder order,
  // then documents in their loaded array order (not sorted by id).
  assert.deepEqual(keys, ['folder:a', 'folder:d', 'knowledge:k2', 'knowledge:k1', 'knowledge:k3'])
})

test('selectionKeys stays sorted for deterministic payloads and is not used for range order', () => {
  const selection = {
    knowledgeIds: new Set(['k2', 'k1']),
    folderIds: new Set(['f2', 'f1']),
  }
  assert.deepEqual(selectionKeys(selection), [
    'folder:f1', 'folder:f2', 'knowledge:k1', 'knowledge:k2',
  ])
})

test('sorts direct folders by locale-aware name', () => {
  assert.deepEqual(
    sortDirectFolders([folder('2', '', 'B', 1), folder('1', '', 'A', 1)]).map((item) => item.id),
    ['1', '2'],
  )
})

test('separates folder and knowledge selection keys and capabilities', () => {
  const selection = {
    knowledgeIds: new Set(['k1', 'k2']),
    folderIds: new Set(['f1']),
  }
  assert.deepEqual(selectionKeys(selection), ['folder:f1', 'knowledge:k1', 'knowledge:k2'])
  assert.deepEqual(selectionCapabilities(selection), {
    canMove: true, canDelete: true, canReparse: true,
  })
})

test('disables self, descendants, and current parent as move targets', () => {
  const index = buildFolderIndex(tree)
  assert.equal(isMoveTargetDisabled(index, new Set(['a']), 'a', ''), true)
  assert.equal(isMoveTargetDisabled(index, new Set(['a']), 'c', ''), true)
  assert.equal(isMoveTargetDisabled(index, new Set(['a']), '', ''), true)
  assert.equal(isMoveTargetDisabled(index, new Set(['a']), 'd', ''), false)
})

test('insertCreatePlaceholder returns nodes unchanged when no create is active', () => {
  const nodes = [
    { id: '', level: 1 },
    { id: 'a', level: 2 },
    { id: 'b', level: 2 },
  ]
  assert.deepEqual(insertCreatePlaceholder(nodes, null), nodes)
})

test('insertCreatePlaceholder inserts a level-2 placeholder after the root for root create', () => {
  const nodes = [
    { id: '', level: 1 },
    { id: 'a', level: 2 },
  ]
  const result = insertCreatePlaceholder(nodes, '')
  assert.equal(result.length, 3)
  assert.deepEqual(result[1], { isPlaceholder: true, level: 2 })
  // Surrounding nodes are preserved (by reference, in order).
  assert.equal(result[0], nodes[0])
  assert.equal(result[2], nodes[1])
})

test('insertCreatePlaceholder inserts a placeholder immediately after the parent node', () => {
  const nodes = [
    { id: '', level: 1 },
    { id: 'a', level: 2 },
    { id: 'b', level: 3 }, // child of a
    { id: 'c', level: 2 },
  ]
  const result = insertCreatePlaceholder(nodes, 'a')
  assert.equal(result.length, 5)
  assert.deepEqual(result[2], { isPlaceholder: true, level: 3 })
  assert.equal(result[1], nodes[1]) // 'a' still at index 1
  assert.equal(result[3], nodes[2]) // 'b' shifted to index 3
  assert.equal(result[4], nodes[3]) // 'c' shifted to index 4
})

test('insertCreatePlaceholder returns nodes unchanged when the parent is not visible', () => {
  const nodes = [{ id: '', level: 1 }, { id: 'a', level: 2 }]
  // 'ghost' is not in the visible (expanded) node list.
  assert.equal(insertCreatePlaceholder(nodes, 'ghost'), nodes)
})
