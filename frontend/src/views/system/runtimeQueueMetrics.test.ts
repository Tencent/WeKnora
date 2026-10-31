import assert from 'node:assert/strict'
import test from 'node:test'

import { batchCountTotal, batchSuccessRatio, queueCurrentState, queueRuntimeTotals, queueTodaySuccess } from './runtimeQueueMetrics'

test('historical archives remain separate while daily success grows', () => {
  const queues = [
    { active: 1, pending: 2, scheduled: 3, retry: 4, processed: 12, failed: 2, archived: 7, completed: 5 },
    { active: 0, pending: 1, scheduled: 0, retry: 1, processed: 4, failed: 1, archived: 7, completed: 0 },
  ] as any
  const totals = queueRuntimeTotals(queues)
  assert.equal(totals.todaySuccess, 13)
  assert.equal(totals.archived, 14)
  assert.equal(totals.retainedCompleted, 5)
})

test('retained archives do not turn an idle or active queue into a current failure', () => {
  const queue = { paused: false, retry: 0, active: 0, pending: 0, scheduled: 0, archived: 24859 }
  assert.equal(queueCurrentState(queue), 'idle')
  assert.equal(queueCurrentState({ ...queue, active: 1 }), 'working')
  assert.equal(queueCurrentState({ ...queue, retry: 1 }), 'retrying')
})

test('daily success resets and never becomes negative when failures exceed processed', () => {
  assert.equal(queueTodaySuccess({ processed: 0, failed: 0 }), 0)
  assert.equal(queueTodaySuccess({ processed: 2, failed: 5 }), 0)
  assert.equal(queueRuntimeTotals([
    { active: 0, pending: 0, scheduled: 0, retry: 0, processed: 0, failed: 0, archived: 4, completed: 0 },
    { active: 0, pending: 0, scheduled: 0, retry: 0, processed: 7, failed: 2, archived: 0, completed: 0 },
  ] as any).todaySuccess, 5)
})

test('batch lifecycle counts, summary state, and success ratio are independent', () => {
  const counts = { waiting_start: 0, queued: 0, processing: 0, finalizing: 0, completed: 2, failed: 1, cancelled: 1, missing: 1, submission_failed: 0, unknown: 0 }
  assert.equal(batchCountTotal(counts), 5)
  assert.equal(batchSuccessRatio({ total: 5, counts }), 0.4)
  assert.equal(batchSuccessRatio({ total: 0, counts }), null)
})

test('batch completion is not retained queue completion', () => {
  const totals = queueRuntimeTotals([{ active: 0, pending: 0, scheduled: 0, retry: 0, processed: 0, failed: 0, archived: 0, completed: 0 }] as any)
  const counts = { waiting_start: 0, queued: 0, processing: 0, finalizing: 0, completed: 3, failed: 0, cancelled: 0, missing: 0, submission_failed: 0, unknown: 0 }
  assert.equal(totals.retainedCompleted, 0)
  assert.equal(batchSuccessRatio({ total: 3, counts }), 1)
})
