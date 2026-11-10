import assert from 'node:assert/strict'
import test from 'node:test'
import {
  RAG_WAIT_REVEAL_DELAY_MS,
  RAG_WAIT_STALL_DELAY_MS,
  createRagWaitController,
  getRagPipelineWaitKind,
  type RagWaitScheduler,
  type RagWaitView,
} from './rag-pipeline-state.ts'

const completedRetrieval = {
  isCompleted: false,
  hasAnswer: false,
  hasThinkingEvent: false,
  stepCount: 2,
  allStepsDone: true,
  hasCompletedRetrievalStep: true,
}

test('waits on the model once retrieval finished', () => {
  assert.equal(getRagPipelineWaitKind(completedRetrieval), 'model')
})

test('waits while a pipeline step is still pending', () => {
  assert.equal(getRagPipelineWaitKind({
    ...completedRetrieval,
    allStepsDone: false,
  }), 'none')
})

test('waits for nothing before pipeline events arrive', () => {
  assert.equal(getRagPipelineWaitKind({
    ...completedRetrieval,
    stepCount: 0,
  }), 'none')
})

test('falls back to the neutral preparing state when no retrieval step ran', () => {
  assert.equal(getRagPipelineWaitKind({
    ...completedRetrieval,
    hasCompletedRetrievalStep: false,
  }), 'preparing')
})

test('stops waiting when thinking, answer, or completion arrives', () => {
  assert.equal(getRagPipelineWaitKind({ ...completedRetrieval, hasThinkingEvent: true }), 'none')
  assert.equal(getRagPipelineWaitKind({ ...completedRetrieval, hasAnswer: true }), 'none')
  assert.equal(getRagPipelineWaitKind({ ...completedRetrieval, isCompleted: true }), 'none')
})

function createFakeScheduler() {
  let now = 0
  let nextHandle = 1
  const jobs = new Map<number, { runAt: number; callback: () => void }>()

  const scheduler: RagWaitScheduler = {
    setTimeout: (callback, ms) => {
      const handle = nextHandle++
      jobs.set(handle, { runAt: now + ms, callback })
      return handle
    },
    clearTimeout: (handle) => {
      jobs.delete(handle as number)
    },
  }

  const advance = (ms: number) => {
    now += ms
    for (const [handle, job] of [...jobs].sort((a, b) => a[1].runAt - b[1].runAt)) {
      if (job.runAt > now) continue
      jobs.delete(handle)
      job.callback()
    }
  }

  return { scheduler, advance, pendingCount: () => jobs.size }
}

function createHarness() {
  const views: RagWaitView[] = []
  const { scheduler, advance, pendingCount } = createFakeScheduler()
  const controller = createRagWaitController((view) => views.push(view), scheduler)
  return { controller, views, advance, pendingCount }
}

test('keeps the wait row hidden while the model answers quickly', () => {
  const { controller, views, advance } = createHarness()

  controller.update('model')
  advance(RAG_WAIT_REVEAL_DELAY_MS - 1)
  controller.update('none')
  advance(RAG_WAIT_STALL_DELAY_MS)

  assert.deepEqual(views, [])
})

test('reveals the wait row once the model stays quiet past the delay', () => {
  const { controller, views, advance } = createHarness()

  controller.update('model')
  advance(RAG_WAIT_REVEAL_DELAY_MS)

  assert.deepEqual(views, [{ kind: 'model', stalled: false }])

  controller.update('none')
  assert.deepEqual(views.at(-1), { kind: 'none', stalled: false })
})

test('marks the wait row stalled when no answer ever arrives', () => {
  const { controller, views, advance } = createHarness()

  controller.update('model')
  advance(RAG_WAIT_REVEAL_DELAY_MS)
  advance(RAG_WAIT_STALL_DELAY_MS - 1)

  assert.deepEqual(views, [{ kind: 'model', stalled: false }])

  advance(1)

  assert.deepEqual(views.at(-1), { kind: 'model', stalled: true })
})

test('swaps the label in place instead of re-running the reveal delay', () => {
  const { controller, views, advance } = createHarness()

  controller.update('preparing')
  advance(RAG_WAIT_REVEAL_DELAY_MS)
  controller.update('model')

  assert.deepEqual(views, [
    { kind: 'preparing', stalled: false },
    { kind: 'model', stalled: false },
  ])
})

test('gives each phase a fresh stall budget', () => {
  const { controller, views, advance } = createHarness()

  controller.update('preparing')
  advance(RAG_WAIT_REVEAL_DELAY_MS)
  advance(RAG_WAIT_STALL_DELAY_MS - 1)
  controller.update('model')
  advance(RAG_WAIT_STALL_DELAY_MS - 1)

  assert.deepEqual(views.at(-1), { kind: 'model', stalled: false })

  advance(1)

  assert.deepEqual(views.at(-1), { kind: 'model', stalled: true })
})

test('drops pending timers on dispose', () => {
  const { controller, views, advance, pendingCount } = createHarness()

  controller.update('model')
  controller.dispose()

  assert.equal(pendingCount(), 0)

  advance(RAG_WAIT_STALL_DELAY_MS)
  assert.deepEqual(views, [])
})
