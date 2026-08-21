/** Pure projections from a tool's canonical value to model-facing text. */

/** Clip text to a character budget, reporting whether anything was dropped. */
export function clip(text: string, maxChars: number): { text: string, truncated: boolean } {
  const normalized = text.replace(/\r\n/g, '\n').trim()
  if (normalized.length <= maxChars) return { text: normalized, truncated: false }
  return { text: `${normalized.slice(0, maxChars).trimEnd()}…`, truncated: true }
}

/** Human-readable score, tolerant of a backend that omits it. */
export function formatScore(score: number): string {
  return Number.isFinite(score) ? score.toFixed(3) : 'n/a'
}

/** Join a list for prose, keeping an empty list explicit. */
export function joinOrNone(values: string[]): string {
  return values.length === 0 ? '(deployment default)' : values.join(', ')
}
