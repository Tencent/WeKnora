// A stage can legitimately run for a while between span writes (one
// DocReader call on a large file, a single LLM call), so this only flags a
// document as possibly stuck; the server fails it after its own threshold.
export const PROCESSING_STALL_THRESHOLD_MS = 20 * 60 * 1000

// Once everything in flight looks stalled, poll at this slower interval.
export const STALLED_POLL_INTERVAL_MS = 15 * 1000

const IN_FLIGHT = new Set(['pending', 'processing', 'finalizing'])

export type ProcessingActivity = {
  parse_status?: string
  last_activity_at?: string | null
}

// stalledMinutes returns how long an in-flight document has gone without
// progress, or 0 while it is progressing, finished, or its activity is unknown.
export function stalledMinutes(item: ProcessingActivity, now: number = Date.now()): number {
  if (!IN_FLIGHT.has(item.parse_status ?? '') || !item.last_activity_at) return 0
  const last = Date.parse(item.last_activity_at)
  if (Number.isNaN(last)) return 0
  const idle = now - last
  return idle >= PROCESSING_STALL_THRESHOLD_MS ? Math.floor(idle / 60000) : 0
}

export type StallVerdict = 'queued' | 'stalled' | ''

// shownStall is the stall verdict to display: the server's stall_state, and
// only while the document is still quiet past the threshold. No verdict
// (not sent, or the server could not probe the queue) shows as ordinary
// processing: calling a backlogged document stuck invites stopping it.
export function shownStall(stallState: string | undefined, minutes: number | undefined): StallVerdict {
  if (!minutes) return ''
  return stallState === 'queued' || stallState === 'stalled' ? stallState : ''
}
