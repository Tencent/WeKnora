import assert from 'node:assert/strict'
import test from 'node:test'

import {
  groupPostprocessGraphSpans,
  isSpansResponseCurrent,
  postprocessElapsedMs,
  summarizePostprocessTasks,
  type KnowledgeTraceNode,
} from './knowledgeTrace.ts'

test('a spans response for the previous document is dropped after switching', () => {
  // Document A's request is in flight when the user opens document B, which
  // issues request 2. A's late response must not render under B.
  const issuedForA = { seq: 1, knowledgeId: 'doc-a', attempt: undefined }
  const nowOnB = { seq: 2, knowledgeId: 'doc-b', attempt: undefined }

  assert.equal(isSpansResponseCurrent(issuedForA, nowOnB), false)
})

test('the latest spans request is applied even though an older one was still in flight', () => {
  const issuedForB = { seq: 2, knowledgeId: 'doc-b', attempt: undefined }
  const nowOnB = { seq: 2, knowledgeId: 'doc-b', attempt: undefined }

  assert.equal(isSpansResponseCurrent(issuedForB, nowOnB), true)
})

test('a latest-attempt request stays current after its own response fills in the attempt', () => {
  // The first fetch for a document sends attempt=undefined ("latest") and
  // the component then records the attempt the server returned.
  const issued = { seq: 3, knowledgeId: 'doc-b', attempt: undefined }
  const afterResponse = { seq: 3, knowledgeId: 'doc-b', attempt: 2 }

  assert.equal(isSpansResponseCurrent(issued, afterResponse), true)
})

test('a spans response for a previously selected attempt is dropped', () => {
  const issuedForAttempt1 = { seq: 4, knowledgeId: 'doc-b', attempt: 1 }
  const nowOnAttempt2 = { seq: 4, knowledgeId: 'doc-b', attempt: 2 }

  assert.equal(isSpansResponseCurrent(issuedForAttempt1, nowOnAttempt2), false)
})

test('a superseded spans request is dropped even when the selection is unchanged', () => {
  // A retry re-issues the same document and attempt; only the newer
  // response reflects the re-parse.
  const older = { seq: 5, knowledgeId: 'doc-b', attempt: undefined }
  const current = { seq: 6, knowledgeId: 'doc-b', attempt: undefined }

  assert.equal(isSpansResponseCurrent(older, current), false)
})

test('postprocess duration follows asynchronous workers instead of enqueue completion', () => {
  const stage: KnowledgeTraceNode = {
    name: 'postprocess', kind: 'stage', status: 'done',
    started_at: '2026-10-25T00:00:00.000Z',
    finished_at: '2026-10-25T00:00:00.032Z',
    duration_ms: 32,
    children: [{
      name: 'postprocess.wiki', kind: 'subspan', status: 'running',
      started_at: '2026-10-25T00:00:01.000Z',
    }],
  }

  const afterEnqueue = Date.parse('2026-10-25T00:02:00.000Z')
  assert.equal(postprocessElapsedMs(stage, afterEnqueue), 120_000)
  assert.equal(stage.duration_ms, 32, 'the stored stage duration still measures enqueue work')

  stage.children![0].status = 'done'
  stage.children![0].finished_at = '2026-10-25T00:03:00.000Z'
  assert.equal(postprocessElapsedMs(stage, afterEnqueue + 60_000), 180_000)

  stage.children = [{ name: 'postprocess.wiki', kind: 'subspan', status: 'pending' }]
  assert.equal(postprocessElapsedMs(stage, afterEnqueue), 120_000,
    'queued workers must not make the stage appear complete')
  stage.children = []
  assert.equal(postprocessElapsedMs(stage, afterEnqueue), null,
    'a stage without asynchronous work keeps its own duration')
})

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

test('summarizes asynchronous postprocess leaf tasks', () => {
  const trace: KnowledgeTraceNode = {
    name: 'root',
    kind: 'root',
    status: 'done',
    children: [
      {
        name: 'postprocess',
        kind: 'stage',
        status: 'done',
        children: [
          { name: 'postprocess.summary', kind: 'subspan', status: 'done' },
          { name: 'postprocess.question', kind: 'subspan', status: 'running' },
          {
            name: 'postprocess.graph',
            kind: 'group',
            status: 'failed',
            children: [
              { name: 'postprocess.graph.chunk[0]', kind: 'subspan', status: 'failed' },
              { name: 'postprocess.graph.chunk[1]', kind: 'subspan', status: 'pending' },
            ],
          },
        ],
      },
    ],
  }

  assert.deepEqual(summarizePostprocessTasks(trace), {
    running: 2,
    failed: 1,
    completed: 1,
    other: 0,
    total: 4,
  })
})

test('returns an empty postprocess summary when no trace is available', () => {
  assert.deepEqual(summarizePostprocessTasks(undefined), {
    running: 0,
    failed: 0,
    completed: 0,
    other: 0,
    total: 0,
  })
})
