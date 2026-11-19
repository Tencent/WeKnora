export type HistoryPageAttempt =
  | { status: 'failed' }
  | {
      status: 'loaded'
      itemCount: number
      pageSize: number
      cursorAdvanced: boolean
    }

export function historyHasMoreAfterAttempt(
  previousHasMore: boolean,
  attempt: HistoryPageAttempt,
): boolean {
  if (attempt.status === 'failed') return previousHasMore
  return previousHasMore && attempt.cursorAdvanced && attempt.itemCount >= attempt.pageSize
}
