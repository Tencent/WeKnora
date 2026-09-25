// 已加载数量小于总数时按页继续请求；总页数限制可以避免异常数据造成无限请求。
export function getNextDocumentPage(
  loadedCount: number,
  totalCount: number,
  currentPage: number,
  pageSize: number,
): number | null {
  if (pageSize <= 0 || loadedCount >= totalCount) return null

  const nextPage = currentPage + 1
  return nextPage <= Math.ceil(totalCount / pageSize) ? nextPage : null
}
