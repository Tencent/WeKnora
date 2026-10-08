/**
 * Type filters for the artifact library page. The server filters by an
 * extension allowlist, so each category is just the extensions it covers.
 */
export type ArtifactCategory = 'all' | 'document' | 'spreadsheet' | 'presentation' | 'image' | 'web' | 'data'

export const ARTIFACT_CATEGORIES: ArtifactCategory[] = [
  'all',
  'document',
  'spreadsheet',
  'presentation',
  'image',
  'web',
  'data',
]

const CATEGORY_EXTENSIONS: Record<Exclude<ArtifactCategory, 'all'>, string[]> = {
  document: ['.pdf', '.doc', '.docx', '.md', '.txt', '.rtf', '.odt'],
  spreadsheet: ['.xlsx', '.xls', '.csv', '.tsv', '.ods'],
  presentation: ['.pptx', '.ppt', '.key', '.odp'],
  image: ['.png', '.jpg', '.jpeg', '.gif', '.svg', '.webp', '.bmp'],
  web: ['.html', '.htm'],
  data: ['.json', '.xml', '.yaml', '.yml', '.zip'],
}

/** Extensions to send for a category; empty for "all". */
export function artifactCategoryExtensions(category: ArtifactCategory): string[] {
  return category === 'all' ? [] : [...CATEGORY_EXTENSIONS[category]]
}

/** Parses a ?type= query value, falling back to "all". */
export function parseArtifactCategory(raw: unknown): ArtifactCategory {
  return typeof raw === 'string' && (ARTIFACT_CATEGORIES as string[]).includes(raw)
    ? (raw as ArtifactCategory)
    : 'all'
}

export type ArtifactDateGroup = 'today' | 'yesterday' | 'last7Days' | 'last30Days' | 'earlier'

/** Buckets an ISO timestamp relative to `now`, by local calendar day. */
export function artifactDateGroup(raw: string, now: Date = new Date()): ArtifactDateGroup {
  const at = new Date(raw)
  if (Number.isNaN(at.getTime())) return 'earlier'
  const startOfDay = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime()
  const days = Math.round((startOfDay(now) - startOfDay(at)) / 86_400_000)
  if (days <= 0) return 'today'
  if (days === 1) return 'yesterday'
  if (days < 7) return 'last7Days'
  if (days < 30) return 'last30Days'
  return 'earlier'
}

/**
 * Groups items into consecutive date sections. Items arrive newest first, so
 * sections come out in order and a page boundary only ever splits the last one.
 */
export function groupArtifactsByDate<T extends { created_at: string }>(
  items: T[],
  now: Date = new Date(),
): { group: ArtifactDateGroup; items: T[] }[] {
  const sections: { group: ArtifactDateGroup; items: T[] }[] = []
  for (const item of items) {
    const group = artifactDateGroup(item.created_at, now)
    const last = sections[sections.length - 1]
    if (last && last.group === group) {
      last.items.push(item)
    } else {
      sections.push({ group, items: [item] })
    }
  }
  return sections
}
