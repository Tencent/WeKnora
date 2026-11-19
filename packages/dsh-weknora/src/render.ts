/** Pure projections from a tool's canonical value to model-facing text. */

/** Clip text to a character budget, reporting whether anything was dropped. */
export function clip(text: string, maxChars: number): { text: string, truncated: boolean } {
  const normalized = text.replace(/\r\n/g, '\n').trim()
  if (normalized.length <= maxChars) return { text: normalized, truncated: false }
  let end = maxChars
  if (end > 0) {
    const code = normalized.charCodeAt(end - 1)
    if (code >= 0xd800 && code <= 0xdbff) {
      end -= 1
    }
  }
  return { text: `${normalized.slice(0, end).trimEnd()}…`, truncated: true }
}

/** Human-readable score, tolerant of a backend that omits it. */
export function formatScore(score: number): string {
  return Number.isFinite(score) ? score.toFixed(3) : 'n/a'
}

/**
 * Name a retrieval scope without spending the model's context on it. A
 * deployment can hold dozens of knowledge bases, and spelling out every id on
 * every search costs far more than the count is worth; the ids stay in the
 * tool's canonical value for anything that needs them.
 */
export function describeScope(ids: string[]): string {
  if (ids.length === 0) return '(deployment default)'
  if (ids.length <= 3) return ids.join(', ')
  return `${ids.length} knowledge bases`
}
