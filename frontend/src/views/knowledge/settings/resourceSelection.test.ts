import assert from 'node:assert/strict'
import test from 'node:test'

import {
  buildResourceIndexes,
  computeResourceCheckStates,
  toggleResourceSelection,
  type SelectionResource,
} from './resourceSelection.ts'

const resources: SelectionResource[] = [
  { external_id: 'root' },
  { external_id: 'folder-a', parent_id: 'root' },
  { external_id: 'folder-b', parent_id: 'root' },
  { external_id: 'file-a1', parent_id: 'folder-a' },
  { external_id: 'file-a2', parent_id: 'folder-a' },
]

test('parent selection checks every loaded descendant', () => {
  const { children } = buildResourceIndexes(resources)
  const states = computeResourceCheckStates(resources, children, ['folder-a'])

  assert.equal(states.get('root'), 'indeterminate')
  assert.equal(states.get('folder-a'), 'checked')
  assert.equal(states.get('file-a1'), 'checked')
  assert.equal(states.get('file-a2'), 'checked')
  assert.equal(states.get('folder-b'), 'unchecked')
})

test('selecting a parent collapses selected descendants into a minimal cover', () => {
  const { children, parents } = buildResourceIndexes(resources)
  const selected = toggleResourceSelection(
    'folder-a',
    ['file-a1', 'file-a2'],
    children,
    parents,
    'indeterminate',
  )

  // An indeterminate click is an uncheck action in the tree UI.
  assert.deepEqual(selected, [])
  const checked = toggleResourceSelection('folder-a', selected, children, parents, 'unchecked')
  assert.deepEqual(checked, ['folder-a'])
})

test('unchecking a nested item splits an ancestor cover without losing siblings', () => {
  const { children, parents } = buildResourceIndexes(resources)
  const selected = toggleResourceSelection(
    'file-a1',
    ['root'],
    children,
    parents,
    'checked',
  )

  assert.deepEqual(new Set(selected), new Set(['folder-b', 'file-a2']))
  const states = computeResourceCheckStates(resources, children, selected)
  assert.equal(states.get('file-a1'), 'unchecked')
  assert.equal(states.get('file-a2'), 'checked')
  assert.equal(states.get('folder-a'), 'indeterminate')
  assert.equal(states.get('folder-b'), 'checked')
})

test('selection helpers terminate safely on malformed cyclic data', () => {
  const cyclic: SelectionResource[] = [
    { external_id: 'root' },
    { external_id: 'a', parent_id: 'root' },
    { external_id: 'b', parent_id: 'a' },
    { external_id: 'a', parent_id: 'b' },
  ]
  const { children, parents } = buildResourceIndexes(cyclic)
  const selected = toggleResourceSelection('a', ['root'], children, parents, 'checked')
  assert.ok(Array.isArray(selected))
})
