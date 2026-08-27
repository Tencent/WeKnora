/** Shared citation tag preprocessing for chat markdown (QA + agent). */

/** Self-closing or unclosed `<kb/>` / `<web/>` tags from model output. */
export const KB_WEB_TAG_RE = /<(?:kb|web)\b[^>]*?\s*\/?>/g
const KB_TAG_ATTR_RE = /<kb\b([^>]*?)\s*\/?>/g
const WEB_TAG_ATTR_RE = /<web\b([^>]*?)\s*\/?>/g

const ATTRIBUTE_REGEX = /([\w-]+)\s*=\s*"([^"]*)"/g
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

/**
 * Hide a citation tag while the typewriter has only emitted part of it.
 *
 * Without this guard, Markdown renders the leading `<` as ordinary text until
 * the closing `>` arrives. Only the unfinished tail is removed; a complete tag
 * continues through the normal citation pipeline.
 */
export function stripIncompleteCitationTag(content: string): string {
  if (!content) return content

  const start = content.lastIndexOf('<')
  if (start < 0) return content

  const tail = content.slice(start)
  if (tail.includes('>')) return content

  const isCitationPrefix = tail === '<'
    || /^<k(?:b(?:\s[\s\S]*)?)?$/i.test(tail)
    || /^<w(?:e(?:b(?:\s[\s\S]*)?)?)?$/i.test(tail)

  return isCitationPrefix ? content.slice(0, start) : content
}

export type CitationKnowledgeRef = {
  id?: string
  knowledge_id?: string
  knowledge_title?: string
  knowledge_filename?: string
  chunk_index?: number
  chunk_type?: string
  knowledge_base_id?: string
  file_type?: string
  preview_enabled?: boolean
  metadata?: Record<string, string>
  wiki_source_documents?: Array<{
    knowledge_id: string
    knowledge_base_id: string
    title: string
    file_type?: string
    preview_enabled?: boolean
  }>
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

function resolveWikiKnowledgeBaseId(
  slug: string,
  refs?: CitationKnowledgeRef[] | null,
): string {
  const readPageIds = new Set<string>()
  const allIds = new Set<string>()
  for (const ref of refs || []) {
    const refSlug = String(ref.metadata?.slug || '').trim()
    const kbId = String(ref.metadata?.knowledge_base_id || '').trim()
    if (refSlug !== slug || !kbId) continue
    allIds.add(kbId)
    if (ref.metadata?.tool === 'wiki_read_page') readPageIds.add(kbId)
  }
  // A page actually read for this answer is more authoritative than earlier
  // search hits for an identically named slug in another KB.
  if (readPageIds.size === 1) return Array.from(readPageIds)[0]
  return allIds.size === 1 ? Array.from(allIds)[0] : ''
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
      const kbId = resolveWikiKnowledgeBaseId(slug, refs)
      const kbAttr = kbId ? ` data-kb-id="${escapeHtml(kbId)}"` : ''
      return `<a href="#" class="wiki-content-link citation-wiki" data-slug="${escapeHtml(slug)}"${kbAttr}>${escapeHtml(display)}</a>`
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

/** Opening/closing fence for GFM fenced code blocks (up to 3 spaces indent). */
const COMPLETE_MARKDOWN_CODE_RE = /(```[\s\S]*?```|~~~[\s\S]*?~~~|`[^`\n]*`)/g

/**
 * Repair a common model-generated Wiki link shape:
 * `[title](summary/<uuid>)`.
 *
 * A summary slug is a Wiki page identity, not a relative web URL. If it is
 * left as an ordinary Markdown link, the browser resolves it against the
 * current chat route (for example `/platform/chat/summary/<uuid>`). Only the
 * generated summary-page UUID form is normalized here; external links and
 * ordinary relative Markdown links are left untouched. Code spans/fences are
 * skipped so examples in the answer are not rewritten.
 */
export function normalizeWikiMarkdownLinks(content: string): string {
  if (!content || !content.includes('summary/')) return content

  const parts = content.split(COMPLETE_MARKDOWN_CODE_RE)
  for (let i = 0; i < parts.length; i += 2) {
    parts[i] = parts[i].replace(
      /(?<!\!)\[([^\]\n]+)\]\(\s*(summary\/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})(?:\s+(?:"[^"\n]*"|'[^'\n]*'|\([^\)\n]*\)))?\s*\)/gi,
      (_match, label: string, slug: string) => `[[${slug}|${label.replace(/\|/g, '｜')}]]`,
    )
  }
  return parts.join('')
}

function escapeAttribute(value: string): string {
  return escapeHtml(value)
}

function sourceDocumentTitle(source: CitationKnowledgeRef | NonNullable<CitationKnowledgeRef['wiki_source_documents']>[number]): string {
  return String(
    ('title' in source ? source.title : '') ||
    ('knowledge_title' in source ? source.knowledge_title : '') ||
    ('knowledge_filename' in source ? source.knowledge_filename : '') ||
    '',
  ).trim()
}

function sourceDocumentIsPreviewable(source: CitationKnowledgeRef | NonNullable<CitationKnowledgeRef['wiki_source_documents']>[number]): boolean {
  return source.preview_enabled === true &&
    Boolean(String(source.knowledge_id || '').trim()) &&
    Boolean(String(source.knowledge_base_id || '').trim())
}

type SourceDocumentCandidate = CitationKnowledgeRef | NonNullable<CitationKnowledgeRef['wiki_source_documents']>[number]

function sourceDocumentCandidates(refs: CitationKnowledgeRef[] | null | undefined) {
  const seen = new Set<string>()
  const candidates: SourceDocumentCandidate[] = []
  for (const ref of refs || []) {
    for (const candidate of [
      ...(ref.wiki_source_documents || []),
      ref,
    ]) {
      if (!sourceDocumentIsPreviewable(candidate)) continue
      const key = `${candidate.knowledge_base_id}:${candidate.knowledge_id}`
      if (seen.has(key)) continue
      seen.add(key)
      candidates.push(candidate)
    }
  }
  return candidates
}

function sourceDocumentMatchesLabel(source: SourceDocumentCandidate, label: string): boolean {
  const normalizedLabel = String(label || '').trim().toLocaleLowerCase()
  const title = sourceDocumentTitle(source).toLocaleLowerCase()
  return Boolean(title) && (
    normalizedLabel === title ||
    normalizedLabel.includes(title) ||
    title.includes(normalizedLabel)
  )
}

function findUniqueSourceDocument(
  candidates: SourceDocumentCandidate[],
  label: string,
): SourceDocumentCandidate | undefined {
  const matches = candidates.filter((source) => sourceDocumentMatchesLabel(source, label))
  return matches.length === 1 ? matches[0] : undefined
}

function renderSourceDocumentLink(label: string, source: SourceDocumentCandidate): string {
  return `<a href="#" class="wiki-source-document-link" data-source-document-title="${escapeAttribute(sourceDocumentTitle(source))}" data-source-document-id="${escapeAttribute(String(source.knowledge_id || ''))}" data-source-document-kb-id="${escapeAttribute(String(source.knowledge_base_id || ''))}" role="button" tabindex="0">${escapeHtml(label)}</a>`
}

function splitMarkdownTableRow(line: string): string[] {
  const cells: string[] = []
  let current = ''
  let wikiLinkDepth = 0
  for (let i = 0; i < line.length; i++) {
    const char = line[i]
    const pair = line.slice(i, i + 2)
    if (pair === '[[') {
      wikiLinkDepth++
      current += pair
      i++
      continue
    }
    if (pair === ']]' && wikiLinkDepth > 0) {
      wikiLinkDepth--
      current += pair
      i++
      continue
    }
    if (char === '|' && wikiLinkDepth === 0 && line[i - 1] !== '\\') {
      cells.push(current)
      current = ''
      continue
    }
    current += char
  }
  cells.push(current)
  return cells
}

function isMarkdownTableSeparator(cells: string[]): boolean {
  const contentCells = cells.filter((cell, index) => {
    if (index === 0 && !cell.trim()) return false
    if (index === cells.length - 1 && !cell.trim()) return false
    return true
  })
  return contentCells.length > 0 && contentCells.every((cell) => /^\s*:?-{3,}:?\s*$/.test(cell))
}

function isSourceDocumentColumn(header: string): boolean {
  const value = header.replace(/[*_`]/g, '').replace(/\s+/g, '').toLocaleLowerCase()
  return /^(原始?文件|源文件)(链接|预览)?$/.test(value) ||
    /^(original|source)(file|document)(link|preview)?$/.test(value)
}

function sourceDocumentTableCellLabel(cell: string): string {
  let label = cell.trim().replace(/^(?:\*\*|__)(.*)(?:\*\*|__)$/, '$1').trim()

  // A model may put a Wiki-style summary link or a regular Markdown link in
  // the original-file column. Resolve the visible label before the generic
  // Wiki-link normalizer gets a chance to turn summary/<uuid> into a Wiki
  // drawer link.
  const wikiMatch = label.match(/^\[\[([^|\]]+)(?:\|([^\]]+))?\]\]$/)
  if (wikiMatch) return (wikiMatch[2] || wikiMatch[1] || '').trim()

  const markdownMatch = label.match(/^\[([^\]\n]+)\]\([^\n]*\)$/)
  if (markdownMatch) return markdownMatch[1].trim()

  return label
}

function linkSourceDocumentTableCells(
  content: string,
  candidates: SourceDocumentCandidate[],
): string {
  if (!candidates.length || !content.includes('|')) return content
  const lines = content.split('\n')
  for (let headerIndex = 0; headerIndex < lines.length - 1; headerIndex++) {
    const headerCells = splitMarkdownTableRow(lines[headerIndex])
    const separatorCells = splitMarkdownTableRow(lines[headerIndex + 1])
    if (headerCells.length < 3 || headerCells.length !== separatorCells.length || !isMarkdownTableSeparator(separatorCells)) continue

    const sourceColumns = headerCells
      .map((header, index) => isSourceDocumentColumn(header) ? index : -1)
      .filter((index) => index >= 0)
    if (!sourceColumns.length) continue

    for (let rowIndex = headerIndex + 2; rowIndex < lines.length; rowIndex++) {
      const cells = splitMarkdownTableRow(lines[rowIndex])
      if (cells.length !== headerCells.length) break
      for (const columnIndex of sourceColumns) {
        const cell = cells[columnIndex]
        const leading = cell.match(/^\s*/)?.[0] || ''
        const trailing = cell.match(/\s*$/)?.[0] || ''
        const rawLabel = cell.trim()
        if (!rawLabel || /^<a\b/i.test(rawLabel)) continue
        const label = sourceDocumentTableCellLabel(rawLabel)
        if (!label) continue
        const source = findUniqueSourceDocument(candidates, label)
        if (source) cells[columnIndex] = `${leading}${renderSourceDocumentLink(label, source)}${trailing}`
      }
      lines[rowIndex] = cells.join('|')
    }
  }
  return lines.join('\n')
}

/**
 * Convert explicit model document citations such as `[paper](d1)` into
 * client-side preview actions. It also repairs a plain verified filename in a
 * clearly labelled original-file table column. This narrowly scoped fallback
 * avoids scanning or rewriting same-name prose elsewhere in the answer.
 *
 * `d1` is a request-local model handle, not a browser URL. Unresolved or
 * ambiguous links become plain text so the browser cannot navigate to a route
 * such as `/platform/chat/d1`.
 */
export function normalizeWikiDocumentMarkdownLinks(
  content: string,
  refs?: CitationKnowledgeRef[] | null,
): string {
  if (!content) return content

  const candidates = sourceDocumentCandidates(refs)
  const parts = content.split(COMPLETE_MARKDOWN_CODE_RE)
  for (let i = 0; i < parts.length; i += 2) {
    parts[i] = parts[i].replace(
      /(?<!\!)\[([^\]\n]+)\]\(\s*(d[1-9][0-9]*)(?:\s+(?:"[^"\n]*"|'[^'\n]*'|\([^\)\n]*\)))?\s*\)/gi,
      (_match, label: string) => {
        const source = findUniqueSourceDocument(candidates, label)
        return source ? renderSourceDocumentLink(label, source) : escapeHtml(label)
      },
    )
    parts[i] = linkSourceDocumentTableCells(parts[i], candidates)
  }
  return parts.join('')
}

const FENCED_CODE_DELIMITER_RE = /^ {0,3}(`{3,}|~{3,})(\s*\S.*)?\s*$/

function isFencedCodeDelimiterLine(line: string): boolean {
  return FENCED_CODE_DELIMITER_RE.test(line)
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

  // Blank lines before citations: join to the previous content. Fenced-code
  // delimiters are the only exception because ``` / ~~~ must stay on their own line.
  result = result.replace(/\n[ \t]*\n+([ \t]*<(?:kb|web)\b)/gi, (match, kbStart, offset, full) => {
    const before = full.slice(0, offset)
    const lastLine = before.split('\n').filter((line: string) => line.trim()).pop() || ''
    if (isFencedCodeDelimiterLine(lastLine)) {
      return `\n\n${kbStart}`
    }
    return ` ${kbStart.trimStart()}`
  })

  // Single newline before citation when it follows text or another citation (not after a blank line)
  result = result.replace(
    /(?<!\n)(<(?:kb|web)\b[^>]*?\s*\/?>|[ \t]*\S[^\n]*?)\n([ \t]*<(?:kb|web)\b)/g,
    (match, beforePart: string, kbStart: string, offset: number, full: string) => {
      // Resolve the full preceding line: lazy capture + lookbehind can grab only a
      // partial line (e.g. ``` captured as ``), which would skip the fence check.
      const lineStart = full.lastIndexOf('\n', offset - 1) + 1
      const fullPrevLine = full.slice(lineStart, offset + beforePart.length)
      if (isFencedCodeDelimiterLine(fullPrevLine)) {
        return match
      }
      return `${beforePart} ${kbStart.trimStart()}`
    },
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
