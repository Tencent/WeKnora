export type TimelineStatusInput = {
  parseStatus?: string
  traceStatus?: string
  isLatestAttempt: boolean
}

/**
 * Convert the knowledge-level parse status to the span status vocabulary used
 * by the processing timeline.
 */
export function parseStatusToTimelineStatus(status?: string): string {
  switch (status) {
    case 'completed':
      return 'done'
    case 'processing':
      return 'running'
    default:
      return status || ''
  }
}

/**
 * The knowledge row is authoritative for the latest attempt. The root span
 * only describes the main parse pipeline and may already be `done` while
 * asynchronous enrichment is still running or has subsequently failed.
 * Historical attempts have no matching knowledge-row snapshot, so they keep
 * their own persisted root-span status.
 */
export function resolveTimelineHeaderStatus(input: TimelineStatusInput): string {
  if (input.isLatestAttempt) {
    return parseStatusToTimelineStatus(input.parseStatus) || input.traceStatus || ''
  }
  return input.traceStatus || parseStatusToTimelineStatus(input.parseStatus)
}
