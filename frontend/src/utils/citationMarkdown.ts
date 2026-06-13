/** Shared citation tag preprocessing for chat markdown (QA + agent). */

const ATTRIBUTE_REGEX = /([\w-]+)\s*=\s*"([^"]*)"/g

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

/** Convert <web/> / <kb/> / [[wiki]] tags into inline citation HTML. */
export function preprocessCitationTags(contentStr: string): string {
  if (!contentStr.trim()) return ''

  return contentStr
    .replace(/<web\b([^>]*)\/>/g, (_m, attrString: string) => {
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
      return `<a class="citation citation-web" data-url="${safeUrl}" href="${safeUrl}" target="_blank" rel="noopener noreferrer"><span class="citation-icon web"></span><span class="citation-domain">${domain}</span><span class="citation-tip"><span class="tip-title">${safeTitle}</span><span class="tip-url">${safeUrl}</span></span></a>`
    })
    .replace(/<kb\b([^>]*)\/>/g, (_m, attrString: string) => {
      const attrs = parseTagAttributes(attrString)
      const doc = attrs.doc || ''
      const chunkId = attrs.chunk_id || attrs.chunkId || ''
      const kbId = attrs.kb_id || attrs.kbId || ''
      if (!doc || !chunkId) return ''

      const safeDoc = escapeHtml(doc)
      const safeKbId = escapeHtml(kbId)
      const safeChunkId = escapeHtml(chunkId)
      const displayDoc = escapeHtml(truncateMiddle(doc))
      return `<span class="citation citation-kb" data-kb-id="${safeKbId}" data-chunk-id="${safeChunkId}" data-doc="${safeDoc}" role="button" tabindex="0"><span class="citation-icon kb"></span><span class="citation-text">${displayDoc}</span><span class="citation-tip"><span class="tip-loading">…</span></span></span>`
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
export function extractCitationHtmlPlaceholders(contentStr: string): { content: string; htmlSnippets: string[] } {
  const htmlSnippets: string[] = []
  const storeHtml = (html: string): string => {
    const idx = htmlSnippets.length
    htmlSnippets.push(html)
    return `@@WEKNORA_HTML_PLACEHOLDER_${idx}@@`
  }

  const content = contentStr
    .replace(/<(?:kb|web)\b[^>]*\/>/g, (match) => storeHtml(preprocessCitationTags(match)))
    .replace(/\[\[([^\]]+)\]\]/g, (match) => storeHtml(preprocessCitationTags(match)))

  return { content, htmlSnippets }
}

export function restoreCitationHtmlPlaceholders(html: string, htmlSnippets: string[]): string {
  if (!htmlSnippets.length) return html
  return html.replace(HTML_PLACEHOLDER_RE, (_match, idx) => htmlSnippets[Number(idx)] || '')
}

/** Preserve raw <kb>/<web> tags before sanitizers that would strip them. */
export function preserveCitationTags(contentStr: string): { text: string; tags: string[] } {
  const tags: string[] = []
  const text = contentStr.replace(/<(?:kb|web)\b[^>]*\/>/g, (match) => {
    const idx = tags.length
    tags.push(match)
    return `\x00TAG${idx}\x00`
  })
  return { text, tags }
}

export function restoreCitationTags(text: string, tags: string[]): string {
  return text.replace(/\x00TAG(\d+)\x00/g, (_, idx) => tags[Number(idx)] || '')
}
