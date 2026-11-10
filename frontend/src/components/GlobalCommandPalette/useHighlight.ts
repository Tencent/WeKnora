/**
 * Safely highlight query keywords inside a plain-text string by wrapping
 * matches in <mark> tags. The input text is HTML-escaped first so the output
 * is safe to bind with v-html.
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
  const escaped = escapeHtml(text || '')
  if (!q) return escaped
  const keywords = q.split(/\s+/).filter(Boolean)
  let result = escaped
  for (const kw of keywords) {
    const escapedKw = escapeHtml(kw).replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
    if (!escapedKw) continue
    const regex = new RegExp(`(${escapedKw})`, 'gi')
    result = result.replace(regex, '<mark class="search-highlight">$1</mark>')
  }
  return result
}
