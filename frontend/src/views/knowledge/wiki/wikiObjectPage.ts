export const WIKI_OBJECT_TYPES = [
  'entity',
  'concept',
  'methodology',
  'case',
  'insight',
] as const

export type WikiObjectType = (typeof WIKI_OBJECT_TYPES)[number]

type WikiPageLike = {
  page_type?: string
  content?: string
}

const wikiObjectTypeSet = new Set<string>(WIKI_OBJECT_TYPES)

function frontmatterLines(content: string): string[] | null {
  let lines = content.replace(/^\uFEFF/, '').trimStart().split(/\r?\n/)
  if (lines.length > 0 && /^```(?:yaml|yml)?\s*$/i.test(lines[0].trim())) {
    lines = lines.slice(1)
  }
  if (lines.length === 0 || lines[0].trim() !== '---') return null

  const end = lines.findIndex((line, index) => index > 0 && line.trim() === '---')
  return end > 0 ? lines.slice(1, end) : null
}

function frontmatterScalar(lines: string[], key: string): string {
  const line = lines.find((candidate) => {
    const match = candidate.match(/^\s*([A-Za-z0-9_-]+)\s*:/)
    return match?.[1] === key
  })
  if (!line) return ''

  const value = line.replace(/^\s*[A-Za-z0-9_-]+\s*:\s*/, '').trim()
  if (!value) return ''
  const withoutComment = value.replace(/\s+#.*$/, '').trim()
  return withoutComment.replace(/^(?:"([\s\S]*)"|'([\s\S]*)')$/, '$1$2').trim()
}

// P4 object pages use the WeKnora-compatible page_type=index and keep their
// product type in frontmatter. Ordinary system index pages do not have one of
// the five accepted object types and must stay out of the content list.
export function getWikiObjectType(page: WikiPageLike): WikiObjectType | null {
  if (page.page_type !== 'index') return null
  const lines = frontmatterLines(page.content || '')
  if (!lines) return null

  const type = frontmatterScalar(lines, 'type')
  const primaryType = frontmatterScalar(lines, 'primary_type')
  if (type && primaryType && type !== primaryType) return null
  const candidate = type || primaryType
  return wikiObjectTypeSet.has(candidate) ? candidate as WikiObjectType : null
}

export function isVisibleWikiContentPage(page: WikiPageLike): boolean {
  return page.page_type !== 'index' || getWikiObjectType(page) !== null
}

export function getWikiPageDisplayType(page: WikiPageLike): string {
  return getWikiObjectType(page) || page.page_type || ''
}
