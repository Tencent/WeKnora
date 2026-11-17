import assert from 'node:assert/strict'
import test from 'node:test'
import { schemaGraphElements } from './schemaGraphPreview.ts'
import type { GraphSchema } from './graphSchema.ts'

test('schema graph uses generated identities, directed parallel edges and self loops', () => {
  const schema: GraphSchema = {
    enabled: true,
    entity_types: [{ name: '<Person "A">' }, { name: 'Company' }],
    relation_types: [
      { name: 'WORKS_AT', source_type: '<Person "A">', target_type: 'Company' },
      { name: 'OWNS', source_type: '<Person "A">', target_type: 'Company' },
      { name: 'KNOWS', source_type: '<Person "A">', target_type: '<Person "A">' },
    ],
  }
  const elements = schemaGraphElements(schema)
  assert.equal(elements.length, 5)
  assert.equal(elements[0].data.id, 'entity-0')
  assert.equal(elements[0].data.name, schema.entity_types[0].name)
  assert.notEqual(elements[0].data.color, elements[1].data.color)
  assert.deepEqual(elements.slice(2).map(edge => [edge.data.id, edge.data.source, edge.data.target]), [
    ['relation-0', 'entity-0', 'entity-1'],
    ['relation-1', 'entity-0', 'entity-1'],
    ['relation-2', 'entity-0', 'entity-0'],
  ])
  assert.equal(elements[2].data.sourceName, schema.entity_types[0].name)
  assert.equal(elements[2].data.targetName, 'Company')
})

test('invalid schemas never become incomplete canvas graphs', () => {
  assert.deepEqual(schemaGraphElements({
    enabled: true, entity_types: [{ name: 'Person' }],
    relation_types: [{ name: 'WORKS_AT', source_type: 'Person', target_type: 'Missing' }],
  }), [])
})
