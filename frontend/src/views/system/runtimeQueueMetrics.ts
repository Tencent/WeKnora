import type { QueueStat, ReparseBatch, ReparseBatchCounts } from '@/api/system'

export function queueTodaySuccess(queue: Pick<QueueStat, 'processed' | 'failed'>): number {
  return Math.max((queue.processed || 0) - (queue.failed || 0), 0)
}

export function queueTodayFailedAttempts(queue: Pick<QueueStat, 'failed'>): number {
  return Math.max(queue.failed || 0, 0)
}

export function queueCurrentState(queue: Pick<QueueStat, 'paused' | 'retry' | 'active' | 'pending' | 'scheduled'>) {
  if (queue.paused) return 'paused'
  if (queue.retry > 0) return 'retrying'
  if (queue.active > 0) return 'working'
  if (queue.pending > 0 || queue.scheduled > 0) return 'waiting'
  return 'idle'
}

export function queueRuntimeTotals(queues: QueueStat[]) {
  return queues.reduce((totals, queue) => ({
    active: totals.active + Math.max(queue.active || 0, 0),
    pending: totals.pending + Math.max(queue.pending || 0, 0),
    scheduled: totals.scheduled + Math.max(queue.scheduled || 0, 0),
    retry: totals.retry + Math.max(queue.retry || 0, 0),
    todaySuccess: totals.todaySuccess + queueTodaySuccess(queue),
    todayFailedAttempts: totals.todayFailedAttempts + queueTodayFailedAttempts(queue),
    archived: totals.archived + Math.max(queue.archived || 0, 0),
    retainedCompleted: totals.retainedCompleted + Math.max(queue.completed || 0, 0),
  }), { active: 0, pending: 0, scheduled: 0, retry: 0, todaySuccess: 0, todayFailedAttempts: 0, archived: 0, retainedCompleted: 0 })
}

export function batchCountTotal(counts: ReparseBatchCounts): number {
  return Object.values(counts).reduce((total, value) => total + Math.max(value || 0, 0), 0)
}

export function batchCompleted(batch: Pick<ReparseBatch, 'counts'>): number {
  return Math.max(batch.counts.completed || 0, 0)
}

export function batchSuccessRatio(batch: Pick<ReparseBatch, 'total' | 'counts'>): number | null {
  if (batch.total <= 0) return null
  return batchCompleted(batch) / batch.total
}
