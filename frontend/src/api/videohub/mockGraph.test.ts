import assert from 'node:assert/strict'
import test from 'node:test'
import { filterMockGraph, mockGraphEdges, mockGraphNodes } from './mockGraph'

test('knowledge graph mock exposes the required full dataset', () => {
  const payload = filterMockGraph()
  assert.equal(mockGraphNodes.length, 20)
  assert.equal(mockGraphEdges.length, 30)
  assert.equal(payload.nodes.length, 20)
  assert.equal(payload.edges.length, 29)
  assert.equal(payload.meta.truncated, false)
  assert.ok(payload.attributes.length >= 6)
})

test('knowledge graph filtering keeps only visible nodes and their internal edges', () => {
  const payload = filterMockGraph({ types: ['概念'] })
  const ids = new Set(payload.nodes.map(node => node.id))
  assert.equal(payload.nodes.length, 4)
  assert.ok(payload.nodes.every(node => node.attributes[0] === '概念'))
  assert.ok(payload.edges.every(edge => ids.has(edge.source) && ids.has(edge.target)))
})

test('knowledge graph limit reports truncation without losing the full total', () => {
  const payload = filterMockGraph({ limit: 8 })
  assert.equal(payload.nodes.length, 8)
  assert.equal(payload.meta.total, 20)
  assert.equal(payload.meta.returned, 8)
  assert.equal(payload.meta.truncated, true)
})
