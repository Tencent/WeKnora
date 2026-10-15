import hljs from 'highlight.js'
import { Renderer } from 'marked'

import { domPurifyAllowedUriRegexp } from './markdownDomPurify.ts'
import { createPreviewMarkdownParser, preprocessMathDelimiters } from './documentPreviewMarkdownParser.ts'
import { escapeHTML, safeMarkdownToHTML, sanitizeDocumentPreviewHTML } from './security.ts'

const PREVIEW_IMAGE_ATTRIBUTES = 'loading="lazy" decoding="async" fetchpriority="low"'
const HIGHLIGHT_AUTO_MAX_CHARS = 32_768

export function isSafePreviewImageHref(href: unknown): href is string {
  if (typeof href !== 'string') return false
  const trimmed = href.trim()
  if (!trimmed || trimmed.startsWith('#')) return false
  return domPurifyAllowedUriRegexp.test(trimmed)
}

function highlightPreviewCode(source: string, lang?: string): string {
  if (source.length > HIGHLIGHT_AUTO_MAX_CHARS) return escapeHTML(source)
  if (lang && hljs.getLanguage(lang)) {
    try {
      return hljs.highlight(source, { language: lang }).value
    } catch {
      // Fall through to auto-detect or plain text.
    }
  }
  return hljs.highlightAuto(source).value
}

function createPreviewMarkdownRenderer(): Renderer {
  const renderer = new Renderer()
  renderer.image = ({ href, title, text }) => {
    if (!isSafePreviewImageHref(href)) return ''
    const safeHref = href.trim().replace(/"/g, '&quot;')
    const safeTitle = title ? ` title="${escapeHTML(title)}"` : ''
    return `<img src="${safeHref}" alt="${escapeHTML(text || '')}"${safeTitle} class="markdown-image" ${PREVIEW_IMAGE_ATTRIBUTES}>`
  }
  renderer.code = ({ text, lang }) => {
    const source = typeof text === 'string' ? text : ''
    return `<pre><code class="hljs">${highlightPreviewCode(source, lang)}</code></pre>`
  }
  return renderer
}

const previewMarked = createPreviewMarkdownParser({
  renderer: createPreviewMarkdownRenderer(),
})

export function renderDocumentPreviewMarkdown(
  markdown: string,
  sanitize: (html: string) => string = sanitizeDocumentPreviewHTML,
): string {
  const mathSafe = preprocessMathDelimiters(markdown)
  return sanitize(previewMarked.parse(safeMarkdownToHTML(mathSafe)) as string)
}

export function renderDocumentPreviewPlainText(
  text: string,
  sanitize: (html: string) => string = sanitizeDocumentPreviewHTML,
): string {
  return sanitize(`<pre><code>${escapeHTML(text)}</code></pre>`)
}
