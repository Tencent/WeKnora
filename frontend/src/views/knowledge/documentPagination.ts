export type DocumentPaginationViewport = {
  loadedCount: number
  totalCount: number
  pageSize: number
  scrollTop: number
  scrollHeight: number
  clientHeight: number
  threshold?: number
}

export type DocumentPaginationState = {
  page: number
  loading: boolean
  generation: number
}

export type DocumentPageRequest = {
  page: number
  generation: number
}

export type DocumentPageRequestResult = 'loaded' | 'failed' | 'stale'

export function createDocumentPaginationState(): DocumentPaginationState {
  return { page: 1, loading: false, generation: 0 }
}

// 重置时递增代次，使旧筛选条件下尚未完成的翻页请求无法推进新列表的页码。
export function resetDocumentPagination(state: DocumentPaginationState): void {
  state.page = 1
  state.loading = false
  state.generation += 1
}

export function shouldLoadNextDocumentPage(viewport: DocumentPaginationViewport): boolean {
  if (viewport.pageSize <= 0 || viewport.loadedCount >= viewport.totalCount) return false

  const threshold = Math.max(0, viewport.threshold ?? 0)
  return viewport.scrollTop + viewport.clientHeight >= viewport.scrollHeight - threshold
}

// 只有确实需要下一页时才占用加载锁；force 用于底部观察点和手动重试。
export function beginDocumentPageRequest(
  state: DocumentPaginationState,
  viewport: DocumentPaginationViewport,
  force = false,
): DocumentPageRequest | null {
  if (state.loading) return null

  const hasMore = viewport.pageSize > 0 &&
    viewport.loadedCount < viewport.totalCount &&
    state.page < Math.ceil(viewport.totalCount / viewport.pageSize)
  if (!hasMore || (!force && !shouldLoadNextDocumentPage(viewport))) return null

  state.loading = true
  return { page: state.page + 1, generation: state.generation }
}

// 请求失败时保持原页码，下一次仍请求同一页；过期请求不得修改当前分页状态。
export function settleDocumentPageRequest(
  state: DocumentPaginationState,
  request: DocumentPageRequest,
  succeeded: boolean,
): DocumentPageRequestResult {
  if (request.generation !== state.generation) return 'stale'

  state.loading = false
  if (!succeeded) return 'failed'

  state.page = request.page
  return 'loaded'
}
