export function pageCount(totalItems: number, pageSize: number): number {
  if (pageSize <= 0) return 1
  return Math.max(1, Math.ceil(Math.max(0, totalItems) / pageSize))
}

export function clampPage(currentPage: number, totalItems: number, pageSize: number): number {
  return Math.min(Math.max(1, Math.trunc(currentPage) || 1), pageCount(totalItems, pageSize))
}

export function pageItems<T>(items: readonly T[], currentPage: number, pageSize: number): T[] {
  if (pageSize <= 0) return []
  const page = clampPage(currentPage, items.length, pageSize)
  const offset = (page - 1) * pageSize
  return items.slice(offset, offset + pageSize)
}
