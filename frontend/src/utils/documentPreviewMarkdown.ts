import hljs from 'highlight.js'
import { Marked, Renderer } from 'marked'
import markedKatex from 'marked-katex-extension'
import { escapeHTML, isValidImageURL, safeMarkdownToHTML, sanitizeDocumentPreviewHTML } from './security'

const PREVIEW_IMAGE_ATTRIBUTES = 'loading="lazy" decoding="async" fetchpriority="low"'

function isSafePreviewImageHref(href: unknown): href is string {
  if (typeof href !== 'string' || !href.trim()) return false
  if (isValidImageURL(href)) return true
  const trimmed = href.trim()
  return !trimmed.startsWith('//') && !trimmed.startsWith('#') && !/^[a-z][a-z\d+.-]*:/i.test(trimmed)
}

function createPreviewMarkdownRenderer(): Renderer {
  const renderer = new Renderer()
  renderer.image = ({ href, title, text }) => {
    if (!isSafePreviewImageHref(href)) return ''
    const safeHref = href.replace(/"/g, '&quot;')
    const safeTitle = title ? ` title="${escapeHTML(title)}"` : ''
    return `<img src="${safeHref}" alt="${escapeHTML(text || '')}"${safeTitle} class="markdown-image" ${PREVIEW_IMAGE_ATTRIBUTES}>`
  }
  renderer.code = ({ text, lang }) => {
    const source = typeof text === 'string' ? text : ''
    const highlighted = lang && hljs.getLanguage(lang)
      ? (() => { try { return hljs.highlight(source, { language: lang }).value } catch { return hljs.highlightAuto(source).value } })()
      : hljs.highlightAuto(source).value
    return `<pre><code class="hljs">${highlighted}</code></pre>`
  }
  return renderer
}

export function renderDocumentPreviewMarkdown(markdown: string, sanitize: (html: string) => string = sanitizeDocumentPreviewHTML): string {
  const marked = new Marked({ breaks: true, gfm: true })
  marked.use(markedKatex({ throwOnError: false, nonStandard: true }))
  const mathSafe = markdown.replace(/\\\[([\s\S]*?)\\\]/g, '$$$$$1$$$$').replace(/\\\(([\s\S]*?)\\\)/g, '$$$1$$')
  return sanitize(marked.parse(safeMarkdownToHTML(mathSafe), { renderer: createPreviewMarkdownRenderer() }) as string)
}
