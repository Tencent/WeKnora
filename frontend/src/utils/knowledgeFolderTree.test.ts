import assert from 'node:assert/strict'
import test from 'node:test'

import type { KnowledgeFolder } from '@/types/knowledgeFolder'
import { flattenKnowledgeFolders } from './knowledgeFolderTree.ts'

function folder(
  id: string,
  name: string,
  parentID: string | null = null,
): KnowledgeFolder {
  return {
    id,
    knowledge_base_id: 'kb-1',
    parent_id: parentID,
    name,
    path: `/${id}`,
    depth: 1,
    created_at: '',
    updated_at: '',
  }
}

test('flattens folders in sibling-name and depth-first order', () => {
  const rows = flattenKnowledgeFolders([
    folder('root-b', 'Beta'),
    folder('child-a', 'Child A', 'root-a'),
    folder('root-a', 'Alpha'),
    folder('child-b', 'Child B', 'root-a'),
  ])

  assert.deepEqual(
    rows.map(row => [row.folder.id, row.depth]),
    [
      ['root-a', 0],
      ['child-a', 1],
      ['child-b', 1],
      ['root-b', 0],
    ],
  )
})

test('keeps orphaned and cyclic legacy rows visible without looping', () => {
  const rows = flattenKnowledgeFolders([
    folder('orphan', 'Legacy', 'missing'),
    folder('cycle-a', 'Cycle A', 'cycle-b'),
    folder('cycle-b', 'Cycle B', 'cycle-a'),
  ])

  assert.deepEqual(
    rows.map(row => row.folder.id),
    ['cycle-a', 'cycle-b', 'orphan'],
  )
})
