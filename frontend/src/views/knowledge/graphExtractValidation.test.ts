import assert from 'node:assert/strict'
import test from 'node:test'
import { isGraphExtractConfigComplete, type GraphExtractConfig } from './graphExtractValidation'

test('allows an empty graph extraction config while extraction is disabled', () => {
  const config: GraphExtractConfig = {
    enabled: false,
    text: '',
    tags: [],
    nodes: [],
    relations: []
  }

  assert.equal(isGraphExtractConfigComplete(config), true)
})

test('rejects an enabled graph extraction config with empty sample fields', () => {
  const config: GraphExtractConfig = {
    enabled: true,
    text: '',
    tags: [],
    nodes: [],
    relations: []
  }

  assert.equal(isGraphExtractConfigComplete(config), false)
})

test('accepts a complete enabled graph extraction config', () => {
  const config: GraphExtractConfig = {
    enabled: true,
    text: 'Alice works at Acme.',
    tags: ['works_at'],
    nodes: [
      { name: 'Alice', attributes: [] },
      { name: 'Acme', attributes: [] }
    ],
    relations: [
      { node1: 'Alice', node2: 'Acme', type: 'works_at' }
    ]
  }

  assert.equal(isGraphExtractConfigComplete(config), true)
})
