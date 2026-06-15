/** Shared citation tag preprocessing for chat markdown (QA + agent). */

/** Self-closing or unclosed `<kb/>` / `<web/>` tags from model output. */
export const KB_WEB_TAG_RE = /<(?:kb|web)\b[^>]*?\s*\/?>/g
const KB_TAG_ATTR_RE = /<kb\b([^>]*?)\s*\/?>/g
const WEB_TAG_ATTR_RE = /<web\b([^>]*?)\s*\/?>/g

const ATTRIBUTE_REGEX = /([\w-]+)\s*=\s*"([^"]*)"/g
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

export type CitationKnowledgeRef = {
  id?: string
  knowledge_id?: string
  knowledge_title?: string
  knowledge_filename?: string
  chunk_index?: number
  chunk_type?: string
  knowledge_base_id?: string
}

function parseTagAttributes(attrString: string): Record<string, string> {
  const attributes: Record<string, string> = {}
  if (!attrString) return attributes
  ATTRIBUTE_REGEX.lastIndex = 0
  let match: RegExpExecArray | null
  while ((match = ATTRIBUTE_REGEX.exec(attrString)) !== null) {
    attributes[match[1]] = match[2]
  }
  return attributes
}

function escapeHtml(text: string): string {
  return String(text)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

function truncateMiddle(text: string, maxLength = 13): string {
  if (!text) return ''
  if (text.length <= maxLength) return text
  const half = Math.floor((maxLength - 3) / 2)
  const start = text.slice(0, half + ((maxLength - 3) % 2))
  const end = text.slice(-half)
  return `${start}...${end}`
}

function normalizeDocTitle(title: string): string {
  return title.trim().toLowerCase()
}

function docTitlesMatch(a: string, b: string): boolean {
  if (!a || !b) return false
  const na = normalizeDocTitle(a)
  const nb = normalizeDocTitle(b)
  return na === nb || na.includes(nb) || nb.includes(na)
}

/** Map model context index (1, FAQ-1, DOC-2) to the real chunk UUID from retrieval results. */
export function resolveCitationChunkId(
  rawChunkId: string,
  attrs: { doc?: string; kbId?: string },
  refs?: CitationKnowledgeRef[] | null,
): string {
  const raw = String(rawChunkId || '').trim()
  if (!raw || UUID_RE.test(raw)) return raw

  const list = (refs || []).filter((r) => r && r.chunk_type !== 'web_search')
  if (!list.length) return raw

  const doc = (attrs.doc || '').trim()
  const kbId = (attrs.kbId || '').trim()

  if (doc) {
    const byDoc = list.find(
      (r) =>
        docTitlesMatch(doc, r.knowledge_title || '') ||
        docTitlesMatch(doc, r.knowledge_filename || ''),
    )
    if (byDoc?.id) return byDoc.id
  }

  const faqMatch = raw.match(/^FAQ-(\d+)$/i)
  if (faqMatch) {
    const faqRefs = list.filter((r) => r.chunk_type === 'faq')
    const hit = faqRefs[parseInt(faqMatch[1], 10) - 1]
    if (hit?.id) return hit.id
  }

  const docMatch = raw.match(/^DOC-(\d+)$/i)
  if (docMatch) {
    const docRefs = list.filter((r) => r.chunk_type !== 'faq')
    const hit = docRefs[parseInt(docMatch[1], 10) - 1]
    if (hit?.id) return hit.id
  }

  const num = parseInt(raw, 10)
  if (!Number.isNaN(num) && String(num) === raw) {
    const byPos = list[num - 1]
    if (byPos?.id) return byPos.id
    const byChunkIndex = list.find((r) => r.chunk_index === num || r.chunk_index === num - 1)
    if (byChunkIndex?.id) return byChunkIndex.id
  }

  if (kbId) {
    const scoped = list.filter((r) => r.knowledge_base_id === kbId)
    if (doc) {
      const byDoc = scoped.find(
        (r) =>
          docTitlesMatch(doc, r.knowledge_title || '') ||
          docTitlesMatch(doc, r.knowledge_filename || ''),
      )
      if (byDoc?.id) return byDoc.id
    }
    if (scoped.length === 1 && scoped[0].id) return scoped[0].id
  }

  return raw
}

/** Convert <web/> / <kb/> / [[wiki]] tags into inline citation HTML. */
export function preprocessCitationTags(
  contentStr: string,
  refs?: CitationKnowledgeRef[] | null,
): string {
  if (!contentStr.trim()) return ''

  return contentStr
    .replace(WEB_TAG_ATTR_RE, (_m, attrString: string) => {
      const attrs = parseTagAttributes(attrString)
      const url = attrs.url || ''
      const title = attrs.title || ''
      if (!url) return ''

      let domain = url
      try {
        const u = new URL(url)
        const host = u.hostname || ''
        const parts = host.split('.')
        domain = parts.length >= 2 ? parts.slice(-2).join('.') : host || url
      } catch {
        // keep original
      }
      const safeTitle = escapeHtml(title)
      const safeUrl = escapeHtml(url)
      return `<a class="citation citation-web" data-url="${safeUrl}" href="${safeUrl}" target="_blank" rel="noopener noreferrer"><span class="citation-icon citation-icon--web" aria-hidden="true"></span><span class="citation-domain">${domain}</span><span class="citation-tip"><span class="tip-title">${safeTitle}</span><span class="tip-url">${safeUrl}</span></span></a>`
    })
    .replace(KB_TAG_ATTR_RE, (_m, attrString: string) => {
      const attrs = parseTagAttributes(attrString)
      const doc = attrs.doc || ''
      const rawChunkId = attrs.chunk_id || attrs.chunkId || ''
      const kbId = attrs.kb_id || attrs.kbId || ''
      const chunkId = resolveCitationChunkId(rawChunkId, { doc, kbId }, refs)
      if (!doc || !chunkId) return ''

      const safeDoc = escapeHtml(doc)
      const safeKbId = escapeHtml(kbId)
      const safeChunkId = escapeHtml(chunkId)
      const displayDoc = escapeHtml(truncateMiddle(doc))
      return `<span class="citation citation-kb" data-kb-id="${safeKbId}" data-chunk-id="${safeChunkId}" data-doc="${safeDoc}" role="button" tabindex="0"><span class="citation-icon citation-icon--book" aria-hidden="true"></span><span class="citation-text">${displayDoc}</span><span class="citation-tip"><span class="tip-loading">…</span></span></span>`
    })
    .replace(/\[\[([^\]]+)\]\]/g, (match, inner: string) => {
      const pipeIdx = inner.indexOf('|')
      const slug = pipeIdx > 0 ? inner.substring(0, pipeIdx).trim() : inner.trim()
      if (!slug) return match
      let display = slug
      if (pipeIdx > 0) {
        display = inner.substring(pipeIdx + 1).trim()
      } else {
        const parts = slug.split('/')
        display = parts.length > 1 ? parts.slice(1).join('/') : slug
      }
      return `<a href="#" class="wiki-content-link citation-wiki" data-slug="${escapeHtml(slug)}">${escapeHtml(display)}</a>`
    })
}

const HTML_PLACEHOLDER_RE = /@@WEKNORA_HTML_PLACEHOLDER_(\d+)@@/g

/** Protect citation HTML from markdown parser; restore after marked.parse. */
export function extractCitationHtmlPlaceholders(
  contentStr: string,
  refs?: CitationKnowledgeRef[] | null,
): { content: string; htmlSnippets: string[] } {
  const htmlSnippets: string[] = []
  const storeHtml = (html: string): string => {
    const idx = htmlSnippets.length
    htmlSnippets.push(html)
    return `@@WEKNORA_HTML_PLACEHOLDER_${idx}@@`
  }

  const content = contentStr
    .replace(KB_WEB_TAG_RE, (match) => storeHtml(preprocessCitationTags(match, refs)))
    .replace(/\[\[([^\]]+)\]\]/g, (match) => storeHtml(preprocessCitationTags(match, refs)))

  return { content, htmlSnippets }
}

export function restoreCitationHtmlPlaceholders(html: string, htmlSnippets: string[]): string {
  if (!htmlSnippets.length) return html
  return html.replace(HTML_PLACEHOLDER_RE, (_match, idx) => htmlSnippets[Number(idx)] || '')
}

/** Collapse newlines around <kb/> / <web/> so marked keeps citations inline. */
export function joinCitationTagsToPreviousLine(content: string): string {
  if (!content) return content

  let result = content

  // Newlines between consecutive citation tags
  let prev = ''
  while (result !== prev) {
    prev = result
    result = result.replace(
      /(<(?:kb|web)\b[^>]*?\s*\/?>)\s*\n+\s*(<(?:kb|web)\b)/gi,
      '$1 $2',
    )
  }

  // Blank lines before citations: join to previous prose, but keep a break after lists
  result = result.replace(/\n[ \t]*\n+([ \t]*<(?:kb|web)\b)/gi, (match, kbStart, offset, full) => {
    const before = full.slice(0, offset)
    const lastLine = before.split('\n').filter((line) => line.trim()).pop() || ''
    const isListItem = /^\s*(\d+\.|[-*+])\s+\S/.test(lastLine)
    return isListItem ? `\n\n${kbStart}` : ` ${kbStart}`
  })

  // Single newline before citation when it follows text or another citation (not after a blank line)
  result = result.replace(
    /(?<!\n)(<(?:kb|web)\b[^>]*?\s*\/?>|[ \t]*\S[^\n]*?)\n([ \t]*<(?:kb|web)\b)/g,
    '$1 $2',
  )

  return result
}

const CITATION_HTML_FRAGMENT =
  '(?:<span class="citation\\b[^]*?</span>|<a class="citation\\b[^]*?</a>)'

/** Merge citation-only <p> blocks into the preceding paragraph (marked splits on newlines). */
export function collapseStandaloneCitationParagraphs(html: string): string {
  if (!html || !html.includes('citation')) return html

  const mergePattern = new RegExp(
    `(<\\/(?:p|li)>)\\s*(?:<p>\\s*<\\/p>\\s*)*<p>\\s*(${CITATION_HTML_FRAGMENT})\\s*<\\/p>`,
    'g',
  )

  let result = html
  let prev = ''
  while (result !== prev) {
    prev = result
    result = result.replace(mergePattern, (_match, closeTag: string, citation: string) => {
      return ` ${citation}${closeTag}`
    })
  }

  return result
}

/** Preserve raw <kb>/<web> tags before sanitizers that would strip them. */
export function preserveCitationTags(contentStr: string): { text: string; tags: string[] } {
  const tags: string[] = []
  const text = contentStr.replace(KB_WEB_TAG_RE, (match) => {
    const idx = tags.length
    tags.push(match)
    return `\x00TAG${idx}\x00`
  })
  return { text, tags }
}

export function restoreCitationTags(text: string, tags: string[]): string {
  return text.replace(/\x00TAG(\d+)\x00/g, (_, idx) => tags[Number(idx)] || '')
}
