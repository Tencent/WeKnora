export interface LatestRequestGuard {
  begin: () => number
  isLatest: (requestId: number) => boolean
  invalidate: () => void
}

export function createLatestRequestGuard(): LatestRequestGuard {
  let latestRequestId = 0

  return {
    begin() {
      latestRequestId += 1
      return latestRequestId
    },
    isLatest(requestId) {
      return requestId === latestRequestId
    },
    invalidate() {
      latestRequestId += 1
    },
  }
}

export function getLastPage(total: number, pageSize: number): number {
  const safeTotal = Number.isFinite(total) ? Math.max(0, Math.floor(total)) : 0
  const safePageSize = Number.isFinite(pageSize) ? Math.max(1, Math.floor(pageSize)) : 1
  return Math.max(1, Math.ceil(safeTotal / safePageSize))
}
