export type FAQEntryViewMode = 'card' | 'list'

export function parseFAQEntryViewMode(value: string | null): FAQEntryViewMode {
  return value === 'list' ? 'list' : 'card'
}
