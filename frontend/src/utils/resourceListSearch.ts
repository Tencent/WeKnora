/** Match only visible resource metadata; preserve source order and object identity. */
export function matchesResourceQuery(resource: { name?: string; description?: string } | null | undefined, query: string): boolean {
  const terms = query.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean)
  if (!terms.length) return true
  const text = `${resource?.name ?? ''} ${resource?.description ?? ''}`.toLocaleLowerCase()
  return terms.every(term => text.includes(term))
}
