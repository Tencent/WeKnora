/**
 * Safely highlight query keywords inside a plain-text string by wrapping
 * matches in <mark> tags. Matched and unmatched slices of the input are
 * HTML-escaped so the output is safe to bind with v-html.
 *
 * Extracted from the legacy KnowledgeSearch.vue so it can be reused by the
 * global command palette and KB-scoped search bar.
 */
export function escapeHtml(str: string): string {
  return (str || '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#039;')
}

export function highlightRegex(text: string, pattern: string): string {
  const src = text || ''
  const p = (pattern || '').trim()
  const escaped = escapeHtml(src)
  if (!p) return escaped
  try {
    const re = new RegExp(p, 'gi')
    let result = ''
    let lastIndex = 0
    for (const match of src.matchAll(re)) {
      const idx = match.index ?? 0
      result += escapeHtml(src.slice(lastIndex, idx))
      result += `<mark class="search-highlight">${escapeHtml(match[0])}</mark>`
      lastIndex = idx + match[0].length
    }
    result += escapeHtml(src.slice(lastIndex))
    return result
  } catch {
    return escaped
  }
}

export function highlightText(text: string, query: string): string {
  const q = (query || '').trim()
  if (!q) return escapeHtml(text || '')
  // Prefer the longer keyword when several alternatives start at the same index.
  const keywords = q.split(/\s+/).filter(Boolean)
  if (keywords.length === 0) return escapeHtml(text || '')
  const pattern = keywords
    .sort((a, b) => b.length - a.length)
    .map(keyword => keyword.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
    .join('|')
  return highlightRegex(text, pattern)
}
