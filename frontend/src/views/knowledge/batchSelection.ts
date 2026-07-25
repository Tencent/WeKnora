export type BatchSelectableKnowledge = {
  id: string
  parse_status?: string
}

export type BatchSelectionSummary = {
  total: number
  inFlight: number
  rebuildable: number
  completed: number
  failed: number
  cancelled: number
  draft: number
  other: number
}

const IN_FLIGHT_STATUS_VALUES = ['pending', 'processing', 'finalizing']
const IN_FLIGHT_STATUSES = new Set(IN_FLIGHT_STATUS_VALUES)
const KNOWN_STATUSES = new Set([
  ...IN_FLIGHT_STATUS_VALUES,
  'completed',
  'failed',
  'cancelled',
  'draft',
])

export function summarizeBatchSelection(
  items: BatchSelectableKnowledge[],
  selectedIds: Set<string>,
): BatchSelectionSummary {
  const summary: BatchSelectionSummary = {
    total: 0,
    inFlight: 0,
    rebuildable: 0,
    completed: 0,
    failed: 0,
    cancelled: 0,
    draft: 0,
    other: 0,
  }

  for (const item of items) {
    if (!selectedIds.has(item.id)) continue

    summary.total++
    const status = item.parse_status || ''
    if (IN_FLIGHT_STATUSES.has(status)) {
      summary.inFlight++
      continue
    }

    if (status !== 'draft' && status !== 'deleting') {
      summary.rebuildable++
    }
    switch (status) {
      case 'completed':
        summary.completed++
        break
      case 'failed':
        summary.failed++
        break
      case 'cancelled':
        summary.cancelled++
        break
      case 'draft':
        summary.draft++
        break
      default:
        if (!KNOWN_STATUSES.has(status)) summary.other++
    }
  }

  return summary
}
