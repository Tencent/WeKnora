export type WikiToolReference = {
  id: string
  title: string
  content: string
  knowledgeBaseId?: string
  slug?: string
}

function extractBlocks(value: string, tag: string): string[] {
  const pattern = new RegExp(`<${tag}(?:\\s[^>]*)?>([\\s\\S]*?)<\\/${tag}>`, 'gi')
  return Array.from(value.matchAll(pattern), (match) => match[1] || '')
}

function extractTag(value: string, tag: string): string {
  return extractBlocks(value, tag)[0]?.trim() || ''
}

function parseWikiLink(value: string): { slug: string; title: string } {
  const match = String(value || '').match(/\[\[([^|\]]+)(?:\|([^\]]+))?\]\]/)
  const slug = match?.[1]?.trim() || ''
  return {
    slug,
    title: match?.[2]?.trim() || slug,
  }
}

function joinDistinct(parts: string[]): string {
  const result: string[] = []
  for (const part of parts) {
    const value = part.trim()
    if (value && !result.includes(value)) result.push(value)
  }
  return result.join('\n\n')
}

/**
 * Convert the XML-like model payloads emitted by the Wiki read tools into
 * individual drawer cards. The payload is intentionally parsed leniently:
 * page content is Markdown and is not guaranteed to be valid XML.
 */
export function parseWikiToolReferences(
  toolName: string,
  output: unknown,
  toolCallId = toolName,
): WikiToolReference[] {
  if (typeof output !== 'string' || !output.trim()) return []

  if (toolName === 'wiki_search') {
    return extractBlocks(output, 'page').flatMap((page, index) => {
      const { slug, title } = parseWikiLink(extractTag(page, 'link'))
      const content = joinDistinct([
        extractTag(page, 'summary'),
        extractTag(page, 'match_snippet'),
      ])
      if (!slug && !title && !content) return []
      const knowledgeBaseId = extractTag(page, 'knowledge_base_id') || undefined
      return [{
        id: `${toolCallId}:${knowledgeBaseId || 'wiki'}:${slug || index + 1}`,
        title: title || slug || `Wiki ${index + 1}`,
        content,
        knowledgeBaseId,
        slug: slug || undefined,
      }]
    })
  }

  if (toolName === 'wiki_read_page') {
    return extractBlocks(output, 'wiki_page').flatMap((page, index) => {
      const { slug, title } = parseWikiLink(extractTag(page, 'link'))
      // The page body commonly begins with the same introduction stored in
      // summary. Showing both makes the drawer look as if its first paragraph
      // was duplicated, so summary is only a fallback for body-less pages.
      const content = extractTag(page, 'content') || extractTag(page, 'summary')
      if (!slug && !title && !content) return []
      const knowledgeBaseId = extractTag(page, 'knowledge_base_id') || undefined
      return [{
        id: `${toolCallId}:${knowledgeBaseId || 'wiki'}:${slug || index + 1}`,
        title: title || slug || `Wiki ${index + 1}`,
        content,
        knowledgeBaseId,
        slug: slug || undefined,
      }]
    })
  }

  return []
}
