/** Pure projections from a tool's canonical value to model-facing text. */

/** Replace lone surrogates while preserving valid pairs (matched as code points). */
export function wellFormed(text: string): string {
  return text.replace(/[\uD800-\uDFFF]/gu, '\uFFFD')
}

/** Clip text to a character budget, reporting whether anything was dropped. */
export function clip(text: string, maxChars: number): { text: string, truncated: boolean } {
  const normalized = wellFormed(text.replace(/\r\n/g, '\n').trim())
  if (normalized.length <= maxChars) return { text: normalized, truncated: false }
  // Keep the UTF-16 budget, but back up if it would split a surrogate pair.
  const cut = /[\uD800-\uDBFF]/.test(normalized.charAt(maxChars - 1)) ? maxChars - 1 : maxChars
  return { text: `${normalized.slice(0, cut).trimEnd()}…`, truncated: true }
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
