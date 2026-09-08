import assert from 'node:assert/strict'
import test from 'node:test'
import { getAiLearningEvalDetail, getAiLearningEvalGraph, getAiLearningEvalRelatedKnowledge } from './aiLearningEval'

test('offline AI learning fixture preserves the audited object and relation counts', () => {
  const graph = getAiLearningEvalGraph()
  assert.equal(graph.nodes.length, 14)
  assert.equal(graph.edges.length, 15)
  assert.deepEqual(graph.counts?.type_counts, { entity: 1, concept: 2, methodology: 4, case: 3, insight: 4 })
  assert.equal(graph.nodes.filter(node => node.is_orphan).length, 0)
})

test('every fixture page has readable content, evidence and resolvable relations', () => {
  const graph = getAiLearningEvalGraph()
  const pageIds = new Set(graph.nodes.map(node => node.wiki_page_id))
  for (const node of graph.nodes) {
    const detail = getAiLearningEvalDetail(node.wiki_page_id || '')
    assert.ok(detail.detail.core_content)
    assert.ok(detail.detail.structure_fields?.length)
    assert.ok(detail.evidence.length)
  }
  for (const edge of graph.edges) {
    assert.ok(pageIds.has(edge.source))
    assert.ok(pageIds.has(edge.target))
    assert.ok(edge.evidence_ids?.length)
  }
})

test('fixture supports server-shaped type filtering and the video Wiki list', () => {
  const methodology = getAiLearningEvalGraph({ types: ['methodology'] })
  assert.equal(methodology.nodes.length, 4)
  assert.ok(methodology.nodes.every(node => node.knowledge_type === 'methodology'))

  const related = getAiLearningEvalRelatedKnowledge('video-under-test')
  assert.equal(related.anchors.length, 14)
  assert.equal(related.overview?.relation_count, 15)
  assert.equal(related.crossVideoItems.length, 0)
  assert.ok(related.anchors.every(anchor => anchor.evidence?.length))
})
