import assert from 'node:assert/strict'
import test from 'node:test'
import {
  cloneGraphSchema, graphSchemaError, graphSchemaMermaid, renameGraphEntity, removeGraphEntity,
  type GraphSchema,
} from './graphSchema.ts'

const fixture = (): GraphSchema => ({
  enabled: true,
  entity_types: [{ name: 'Person' }, { name: 'Company', description: 'An organization' }],
  relation_types: [{ name: 'WORKS_AT', source_type: 'Person', target_type: 'Company' }],
})

test('schema validates names, endpoint references and unique triples', () => {
  assert.equal(graphSchemaError(fixture()), null)
  assert.equal(graphSchemaError(cloneGraphSchema()), null)
  assert.equal(graphSchemaError({ ...fixture(), entity_types: [] }), 'limits')
  for (const name of ['', ' Person ', 'Person\n']) {
    const s = fixture()
    s.entity_types[0].name = name
    assert.equal(graphSchemaError(s), 'invalidName')
  }
  const duplicate = fixture()
  duplicate.relation_types.push({ ...duplicate.relation_types[0] })
  assert.equal(graphSchemaError(duplicate), 'duplicate')
  const missing = fixture()
  missing.relation_types[0].target_type = 'Unknown'
  assert.equal(graphSchemaError(missing), 'endpoints')
})

test('entity renames update endpoints without mutating the source', () => {
  const original = fixture()
  const renamed = renameGraphEntity(original, 0, 'Employee')
  assert.equal(renamed.relation_types[0].source_type, 'Employee')
  assert.equal(original.entity_types[0].name, 'Person')
  assert.equal(original.relation_types[0].source_type, 'Person')
  assert.deepEqual(removeGraphEntity(original, 0).relation_types, [])
  assert.equal(original.relation_types.length, 1)
})

test('cloning and mode switching retain inactive schema definitions', () => {
  const s = fixture()
  const clone = cloneGraphSchema(s)
  clone.enabled = false
  assert.equal(clone.entity_types.length, 2)
  assert.equal(s.enabled, true)
  clone.entity_types[0].name = 'Other'
  assert.equal(s.entity_types[0].name, 'Person')
})

test('Mermaid uses generated IDs and encodes user labels', () => {
  const s = fixture()
  const source = graphSchemaMermaid(s)
  assert.match(source, /flowchart LR/)
  assert.match(source, /e0 -->\|".*"\| e1/)
  assert.ok(source.includes('WORKS_AT'))
  const hostile = renameGraphEntity(s, 0, '"]\nclick e0 "javascript:alert(1)"')
  assert.equal(graphSchemaMermaid(hostile), '')
  const quoted = renameGraphEntity(s, 0, '<script>"Company"</script>')
  const escaped = graphSchemaMermaid(quoted)
  assert.ok(!escaped.includes('<script>'))
  assert.ok(escaped.includes('#60;'))
})
