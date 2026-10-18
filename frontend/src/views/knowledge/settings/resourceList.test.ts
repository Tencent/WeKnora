import assert from 'node:assert/strict'
import test from 'node:test'

import type { Resource } from '@/api/datasource'
import { dedupeResources } from './resourceList.ts'

function resource(overrides: Partial<Resource> & { external_id: string }): Resource {
  return {
    name: '',
    type: '',
    description: '',
    url: '',
    ...overrides,
  }
}

test('rows sharing an external_id collapse into one, in first-seen order', () => {
  const rows = dedupeResources([
    resource({ external_id: 'base-1' }),
    resource({ external_id: 'doc-1', name: 'Doc' }),
    resource({ external_id: 'base-1', name: 'Base', type: 'base', has_children: true }),
  ])

  assert.deepEqual(rows.map(row => row.external_id), ['base-1', 'doc-1'])
  assert.equal(rows[0].name, 'Base')
  assert.equal(rows[0].type, 'base')
  assert.equal(rows[0].has_children, true)
})

test('the first parent and position win, while blanks are filled from the duplicate', () => {
  const rows = dedupeResources([
    resource({ external_id: 'child', parent_id: 'first', description: '', url: '' }),
    resource({
      external_id: 'child',
      parent_id: 'second',
      description: 'from the duplicate',
      url: 'https://example.com',
    }),
  ])

  assert.equal(rows.length, 1)
  assert.equal(rows[0].parent_id, 'first')
  assert.equal(rows[0].description, 'from the duplicate')
  assert.equal(rows[0].url, 'https://example.com')
})

test('has_children is a union so one listing saying so keeps the expander', () => {
  const [row] = dedupeResources([
    resource({ external_id: 'node', has_children: false }),
    resource({ external_id: 'node', has_children: true }),
  ])

  assert.equal(row.has_children, true)
})

test('rows without an external_id are kept rather than silently hidden', () => {
  const rows = dedupeResources([
    resource({ external_id: '' }),
    resource({ external_id: '' }),
  ])

  assert.equal(rows.length, 2)
})

test('a list without duplicates is returned unchanged, in order', () => {
  const input = [
    resource({ external_id: 'a', name: 'A' }),
    resource({ external_id: 'b', name: 'B' }),
  ]

  assert.deepEqual(dedupeResources(input), input)
})
