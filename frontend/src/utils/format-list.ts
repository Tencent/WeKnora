/** Locale-aware conjunction for short UI lists (e.g. missing config items). */
export function formatLocalizedList(items: string[], locale = 'en-US'): string {
  if (items.length === 0) return ''
  if (typeof Intl !== 'undefined' && typeof Intl.ListFormat !== 'undefined') {
    try {
      return new Intl.ListFormat(locale, { style: 'long', type: 'conjunction' }).format(items)
    } catch {
      // Unsupported locale — fall through.
    }
  }
  return items.join(', ')
}
