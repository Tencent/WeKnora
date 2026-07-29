/**
 * Merge a FAQ page into the currently rendered list while preserving the
 * server order. Offset pagination can overlap when a row is updated between
 * requests, so `id` must remain unique for Vue's card key as well as the UI.
 */
export function mergeFAQEntryPage<T extends { id: number }>(
  currentEntries: T[],
  pageEntries: T[],
): T[] {
  const seen = new Set<number>()
  const merged: T[] = []

  for (const entry of [...currentEntries, ...pageEntries]) {
    if (seen.has(entry.id)) continue
    seen.add(entry.id)
    merged.push(entry)
  }

  return merged
}
