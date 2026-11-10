import type { RuntimeTask } from '@/api/system'

// Cursor anchors can fall back to an earlier still-live task when queue state
// changes between requests. That deliberately favors continuing the list over
// failing, and may return a small overlap. Keep the first occurrence so Vue
// row keys remain unique and the scroll position stays stable.
export function mergeRuntimeTaskPage(
  current: RuntimeTask[],
  incoming: RuntimeTask[],
): RuntimeTask[] {
  const seen = new Set(current.map((task) => task.id))
  const merged = [...current]
  for (const task of incoming) {
    if (seen.has(task.id)) continue
    seen.add(task.id)
    merged.push(task)
  }
  return merged
}
