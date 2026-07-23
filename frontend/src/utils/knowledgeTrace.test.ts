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

test('surfaces a failed graph chunk on the aggregate graph row', () => {
  const stage: KnowledgeTraceNode = {
    span_id: 'postprocess',
    name: 'postprocess',
    kind: 'stage',
    status: 'done',
    children: [graphChunk(0), graphChunk(1, { status: 'failed' })],
  }

  const graph = groupPostprocessGraphSpans(stage).children?.[0]
  assert.equal(graph?.status, 'failed')
  assert.equal(graph?.duration_ms, 3000)
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
