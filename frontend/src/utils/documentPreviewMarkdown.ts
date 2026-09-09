import hljs from 'highlight.js'
import { Marked, Renderer } from 'marked'
import markedKatex from 'marked-katex-extension'

import { domPurifyAllowedUriRegexp } from './markdownDomPurify.ts'
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
  if (lang && hljs.getLanguage(lang)) {
    try {
      return hljs.highlight(source, { language: lang }).value
    } catch {
      // Fall through to auto-detect or plain text.
    }
  }
  if (source.length > HIGHLIGHT_AUTO_MAX_CHARS) {
    return escapeHTML(source)
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

const previewMarked = new Marked({
  breaks: true,
  gfm: true,
  renderer: createPreviewMarkdownRenderer(),
})
previewMarked.use(markedKatex({ throwOnError: false, nonStandard: true }))

function preprocessMathDelimiters(rawText: string): string {
  if (!rawText || typeof rawText !== 'string') return ''
  return rawText
    .replace(/\\\[([\s\S]*?)\\\]/g, '$$$$$1$$$$')
    .replace(/\\\(([\s\S]*?)\\\)/g, '$$$1$$')
}

export function renderDocumentPreviewMarkdown(
  markdown: string,
  sanitize: (html: string) => string = sanitizeDocumentPreviewHTML,
): string {
  const mathSafe = preprocessMathDelimiters(markdown)
  return sanitize(previewMarked.parse(safeMarkdownToHTML(mathSafe)) as string)
}
