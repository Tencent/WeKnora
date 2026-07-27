import assert from 'node:assert/strict'
import test from 'node:test'

import { groupPostprocessGraphSpans, type KnowledgeTraceNode } from './knowledgeTrace.ts'

function graphChunk(index: number, overrides: Partial<KnowledgeTraceNode> = {}): KnowledgeTraceNode {
  return {
    span_id: `graph-${index}`,
    parent_span_id: 'postprocess',
    name: `postprocess.graph.chunk[${index}]`,
    kind: 'subspan',
    status: 'done',
    started_at: `2026-07-21T08:00:0${index}.000Z`,
    finished_at: `2026-07-21T08:00:0${index + 2}.000Z`,
    duration_ms: 2000,
    ...overrides,
  }
}

test('groups graph chunks and reports their wall-clock duration', () => {
  const summary: KnowledgeTraceNode = {
    span_id: 'summary',
    name: 'postprocess.summary',
    kind: 'subspan',
    status: 'done',
  }
  const stage: KnowledgeTraceNode = {
    span_id: 'postprocess',
    name: 'postprocess',
    kind: 'stage',
    status: 'done',
    children: [summary, graphChunk(0), graphChunk(1)],
  }

  const grouped = groupPostprocessGraphSpans(stage)
  assert.equal(grouped.children?.length, 2)
  assert.equal(grouped.children?.[0], summary)

  const graph = grouped.children?.[1]
  assert.equal(graph?.name, 'postprocess.graph')
  assert.equal(graph?.status, 'done')
  assert.equal(graph?.duration_ms, 3000)
  assert.equal(graph?.children?.length, 2)
  assert.deepEqual(graph?.output, {
    chunk_count: 2,
    status_counts: { done: 2 },
  })
})

test('keeps graph group live while any graph chunk is running', () => {
  const stage: KnowledgeTraceNode = {
    span_id: 'postprocess',
    name: 'postprocess',
    kind: 'stage',
    status: 'done',
    children: [
      graphChunk(0),
      graphChunk(1, { status: 'running', finished_at: null, duration_ms: undefined }),
    ],
  }

  const graph = groupPostprocessGraphSpans(stage).children?.[0]
  assert.equal(graph?.status, 'running')
  assert.equal(graph?.finished_at, null)
  assert.equal(graph?.duration_ms, undefined)
})

test('marks the graph group partial when one chunk fails after another succeeds', () => {
  const stage: KnowledgeTraceNode = {
    span_id: 'postprocess',
    name: 'postprocess',
    kind: 'stage',
    status: 'done',
    children: [
      graphChunk(0),
      graphChunk(1, {
        status: 'failed',
        error_code: 'GRAPH_EXTRACT_FAILED',
        error_message: 'model response was not valid JSON',
      }),
    ],
  }

  const graph = groupPostprocessGraphSpans(stage).children?.[0]
  assert.equal(graph?.status, 'partial')
  assert.equal(graph?.duration_ms, 3000)
  assert.deepEqual(graph?.output, {
    chunk_count: 2,
    status_counts: { done: 1, failed: 1 },
    failure_count: 1,
    failures: [{
      stage: 'graph_chunk',
      kind: 'chunk',
      chunk_index: 1,
      error_code: 'GRAPH_EXTRACT_FAILED',
      reason: 'model response was not valid JSON',
    }],
  })
})

test('aggregates partial graph statistics and failure details', () => {
  const stage: KnowledgeTraceNode = {
    span_id: 'postprocess',
    name: 'postprocess',
    kind: 'stage',
    status: 'done',
    children: [
      graphChunk(0, {
        output: {
          nodes_succeeded: 2,
          nodes_failed: 0,
          relations_succeeded: 1,
          relations_failed: 0,
          failure_count: 0,
        },
      }),
      graphChunk(1, {
        status: 'partial',
        output: {
          nodes_succeeded: 1,
          nodes_failed: 1,
          relations_succeeded: 2,
          relations_failed: 1,
          failure_count: 2,
          failures: [
            { stage: 'validation', kind: 'node', item_index: 3, reason: 'entity must not be empty' },
            { stage: 'neo4j_write', kind: 'relation', item_index: 4, reason: 'invalid relationship' },
          ],
        },
      }),
    ],
  }

  const graph = groupPostprocessGraphSpans(stage).children?.[0]
  assert.equal(graph?.status, 'partial')
  assert.deepEqual(graph?.output, {
    chunk_count: 2,
    status_counts: { done: 1, partial: 1 },
    nodes_succeeded: 3,
    nodes_failed: 1,
    relations_succeeded: 3,
    relations_failed: 1,
    failure_count: 2,
    failures: [
      { chunk_index: 1, stage: 'validation', kind: 'node', item_index: 3, reason: 'entity must not be empty' },
      { chunk_index: 1, stage: 'neo4j_write', kind: 'relation', item_index: 4, reason: 'invalid relationship' },
    ],
  })
})

test('keeps the aggregate running until all graph chunks are terminal', () => {
  const stage: KnowledgeTraceNode = {
    span_id: 'postprocess',
    name: 'postprocess',
    kind: 'stage',
    status: 'done',
    children: [
      graphChunk(0, { status: 'failed' }),
      graphChunk(1, { status: 'running', finished_at: null, duration_ms: undefined }),
    ],
  }

  const graph = groupPostprocessGraphSpans(stage).children?.[0]
  assert.equal(graph?.status, 'running')
  assert.equal(graph?.duration_ms, undefined)
})

test('leaves postprocess unchanged when it has no graph chunks', () => {
  const stage: KnowledgeTraceNode = {
    span_id: 'postprocess',
    name: 'postprocess',
    kind: 'stage',
    status: 'done',
    children: [],
  }

  assert.equal(groupPostprocessGraphSpans(stage), stage)
})
