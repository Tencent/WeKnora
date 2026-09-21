/**
 * Export a conversation as a single, self-contained HTML file.
 *
 * The content pipeline deliberately reuses `renderChatMarkdown()` — the same
 * renderer the chat bubbles use — so formulas, mermaid, citations, tables and
 * code blocks keep their semantics. The *styling* is intentionally separate
 * (see `sessionHtmlExportStyles.ts`): the app's chat stylesheets only exist
 * inside Vue `<style scoped>` blocks and cannot style a detached document.
 *
 * Everything needed to build the document string is exported as a pure
 * function so it can be unit-tested in the Node test runner; the browser-only
 * steps (mermaid SVG rendering, protected-image inlining, download) are kept
 * in `buildSessionHtml()` / `exportSessionHtml()` and load their heavy
 * dependencies dynamically.
 */

import type { Renderer } from 'marked'

import { createChatMarkdownRenderer, renderChatMarkdown } from './chatMarkdownRenderer.ts'
import type { CitationKnowledgeRef } from './citationMarkdown.ts'
import { buildProtectedFileRequest, resolveProtectedFileAccess } from './protectedFileAccess.ts'
import {
  createSafeImage,
  escapeHTML,
  isValidImageURL,
  safeMarkdownToHTML,
  sanitizeMarkdownHTML,
} from './security.ts'
import type { SessionExportMessage, SessionMarkdownLabels } from './sessionMarkdown.ts'
import { buildExportCss } from './sessionHtmlExportStyles.ts'

export interface SessionHtmlExportOptions {
  sessionId: string
  title: string
  messages: SessionExportMessage[]
  labels: SessionMarkdownLabels
  exportedAt?: string
  /** BCP-47 tag for the `<html lang>` attribute. */
  lang?: string
}

const EXPORT_CSP =
  "default-src 'none'; img-src data: https: http:; style-src 'unsafe-inline'; font-src data: https://cdn.jsdelivr.net"

const DEFAULT_SANS_STACK =
  '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", sans-serif'
const DEFAULT_MONO_STACK =
  'ui-monospace, SFMono-Regular, "SF Mono", Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace'

/** Skip pathologically large images rather than blowing up the exported file. */
const MAX_INLINE_IMAGE_BYTES = 5 * 1024 * 1024

/**
 * `sanitizeMarkdownHTML()` rewrites every provider image to a placeholder and
 * moves the real path to `data-protected-src`, so this is the exact set to act on.
 */
const PROTECTED_IMAGE_SELECTOR = 'img[data-protected-src]'

function isHttpUrl(value: string): boolean {
  return /^https?:\/\//i.test(value)
}

function listItemText(value: string): string {
  return value.replace(/\s+/g, ' ').trim()
}

function resolveFontStack(variable: string, fallback: string): string {
  if (typeof window === 'undefined') return fallback
  const value = getComputedStyle(document.documentElement).getPropertyValue(variable).trim()
  return value || fallback
}

/**
 * Build a filesystem-safe, self-describing download name.
 * Falls back to the session id when the title has no usable characters.
 */
export function buildExportFileName(
  title: string,
  sessionId: string,
  now: Date = new Date(),
): string {
  const safeTitle = String(title || '')
    // eslint-disable-next-line no-control-regex
    .replace(/[\\/:*?"<>|\u0000-\u001f]/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
    .slice(0, 60)
  const pad = (value: number): string => String(value).padStart(2, '0')
  const stamp =
    `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}`
    + `-${pad(now.getHours())}${pad(now.getMinutes())}`
  const base = safeTitle || `session-${String(sessionId || '').slice(0, 8) || 'export'}`
  return `${base}-${stamp}.html`
}

/**
 * The renderer used for exported answers: identical content pipeline to the
 * chat bubbles, minus the sandbox-artifact cards (exports inline the files
 * instead of rendering an interactive card).
 */
export function createExportRenderer(codeRenderer?: Renderer['code']): Renderer {
  return createChatMarkdownRenderer({
    codeRenderer,
    imageRenderer: ({ href, title, text }) => createSafeImage(href, text || '', title || ''),
    invalidImageHtml: () => '',
    isValidImageUrl: (href) => isValidImageURL(href),
  })
}

function renderReferences(message: SessionExportMessage): string {
  const lines = new Set<string>()
  for (const reference of message.knowledge_references || []) {
    const title = reference.knowledge_title
      || reference.knowledge_filename
      || reference.metadata?.title
      || reference.knowledge_source
      || ''
    const text = listItemText(String(title))
    if (!text) continue
    const source = String(reference.metadata?.url || reference.knowledge_source || '')
    lines.add(
      isHttpUrl(source)
        ? `<li><a href="${escapeHTML(source)}" target="_blank" rel="noopener noreferrer">${escapeHTML(text)}</a></li>`
        : `<li>${escapeHTML(text)}</li>`,
    )
  }
  return [...lines].join('')
}

function renderAttachments(message: SessionExportMessage): string {
  const names = (message.attachments || [])
    .map((attachment) => listItemText(String(attachment.file_name || '')))
    .filter(Boolean)
  return names.map((name) => `<li>${escapeHTML(name)}</li>`).join('')
}

export interface RenderExportMessageOptions {
  renderer?: Renderer
  /**
   * Injectable for tests: DOMPurify has no DOM in the Node test runner and
   * escapes the whole document, which would hide the real rendered markup.
   * The app always uses the default.
   */
  sanitizeHtml?: (html: string) => string
}

/**
 * Render one message into its exported `<section>`.
 *
 * Returns an empty string for roles the export does not carry (tool/system
 * messages) and for messages with nothing to show.
 */
export function renderExportMessage(
  message: SessionExportMessage,
  labels: SessionMarkdownLabels,
  options: RenderExportMessageOptions = {},
): string {
  const role = message.role
  if (role !== 'user' && role !== 'assistant') return ''

  const renderer = options.renderer || createExportRenderer()
  const sanitizeHtml = options.sanitizeHtml || sanitizeMarkdownHTML
  const content = String(message.content || '').trim()
  const attachments = renderAttachments(message)
  const references = renderReferences(message)
  if (!content && !attachments && !references) return ''

  let body = ''
  if (role === 'user') {
    // User turns are plain text in the chat UI, so they stay escaped text here.
    if (content) body = `<div class="export-user-text">${escapeHTML(content)}</div>`
  } else if (content) {
    const html = renderChatMarkdown(content, {
      renderer,
      escapeMarkdown: safeMarkdownToHTML,
      sanitizeHtml,
      streaming: false,
      knowledgeReferences: (message.knowledge_references || []) as unknown as CitationKnowledgeRef[],
    })
    if (html) body = `<div class="export-content">${html}</div>`
  }

  const subBlock = (label: string, items: string): string => (items
    ? `<div class="export-message__sub"><p class="export-message__sub-title">${escapeHTML(label)}</p><ul>${items}</ul></div>`
    : '')

  const messageId = String(message.id || '')
  const idAttribute = messageId ? ` data-message-id="${escapeHTML(messageId)}"` : ''
  const roleLabel = role === 'user' ? labels.user : labels.assistant

  return [
    `<section class="export-message export-message--${role}"${idAttribute}>`,
    `<div class="export-message__role">${escapeHTML(roleLabel)}</div>`,
    body,
    subBlock(labels.attachments, attachments),
    subBlock(labels.references, references),
    '</section>',
  ]
    .filter(Boolean)
    .join('\n')
}

/** Assemble the standalone document. Pure: no DOM, no network. */
export function buildSessionHtmlDocument(options: {
  title: string
  sessionId: string
  exportedAt: string
  labels: SessionMarkdownLabels
  bodyHtml: string
  css: string
  lang?: string
}): string {
  const title = escapeHTML(options.title.trim() || options.sessionId)
  const meta = [
    `${escapeHTML(options.labels.sessionId)}: ${escapeHTML(options.sessionId)}`,
    `${escapeHTML(options.labels.exportedAt)}: ${escapeHTML(options.exportedAt)}`,
  ].join(' · ')

  return [
    '<!DOCTYPE html>',
    `<html lang="${escapeHTML(options.lang || 'en')}">`,
    '<head>',
    '<meta charset="utf-8">',
    '<meta name="viewport" content="width=device-width, initial-scale=1">',
    `<meta http-equiv="Content-Security-Policy" content="${EXPORT_CSP}">`,
    '<meta name="generator" content="WeKnora">',
    `<title>${title} - WeKnora</title>`,
    `<style>${options.css}</style>`,
    '</head>',
    '<body>',
    '<main class="export-page">',
    '<header class="export-header">',
    `<h1>${title}</h1>`,
    `<p class="export-meta">${meta}</p>`,
    '</header>',
    options.bodyHtml,
    '</main>',
    '</body>',
    '</html>',
    '',
  ].join('\n')
}

/** Elements that only drive the live UI; they carry no exported meaning. */
const INTERACTIVE_SELECTORS = [
  '.chat-code-block__actions',
  '.chat-mermaid-block__actions',
  '.citation-tip',
  '.citation-icon',
  '.streaming-image-loading',
]

/** Drop the live-UI chrome and unwrap streaming-only wrappers. */
function stripExportChrome(root: ParentNode): void {
  for (const selector of INTERACTIVE_SELECTORS) {
    root.querySelectorAll(selector).forEach((element) => element.remove())
  }
  root.querySelectorAll('.stream-fade-tail').forEach((element) => {
    element.replaceWith(...Array.from(element.childNodes))
  })
}

async function renderMermaidBlocks(
  root: ParentNode,
  renderSvg: (code: string, id?: string) => Promise<string | null>,
): Promise<void> {
  const blocks = Array.from(
    root.querySelectorAll<HTMLElement>('.chat-mermaid-block__canvas[data-mermaid="false"]'),
  )
  for (const block of blocks) {
    const code = (block.querySelector('code')?.textContent || block.textContent || '').trim()
    if (!code) continue
    const svg = await renderSvg(code, 'mermaid-export')
    // A failed render leaves the highlighted code block in place.
    if (!svg) continue
    block.innerHTML = svg
    block.setAttribute('data-mermaid', 'true')
  }
}

function blobToDataUrl(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result || ''))
    reader.onerror = () => reject(reader.error || new Error('failed to read blob'))
    reader.readAsDataURL(blob)
  })
}

function replaceWithMissingPlaceholder(image: HTMLImageElement): void {
  const placeholder = document.createElement('span')
  placeholder.className = 'export-image-missing'
  placeholder.textContent = image.getAttribute('alt') || image.getAttribute('title') || 'image'
  image.replaceWith(placeholder)
}

/**
 * A downloaded file cannot send the Bearer token, so every protected image has
 * to become a `data:` URL. Blob URLs are equally useless once the page closes.
 */
async function inlineProtectedImages(root: ParentNode, sessionId: string): Promise<void> {
  const images = Array.from(root.querySelectorAll<HTMLImageElement>(PROTECTED_IMAGE_SELECTOR))
  await Promise.all(images.map(async (image) => {
    const source = (image.getAttribute('data-protected-src') || '').trim()
    if (!source) return

    const messageId = image.closest('[data-message-id]')?.getAttribute('data-message-id') || ''
    const access = resolveProtectedFileAccess(
      sessionId && messageId ? { mode: 'message', sessionId, messageId } : null,
    )
    const request = buildProtectedFileRequest(source, access)
    // Not a storage-backed path (plain https/data image): leave it untouched.
    if (!request) return

    try {
      const response = await fetch(request.url, {
        method: 'GET',
        headers: request.headers,
        credentials: 'include',
      })
      if (!response.ok) throw new Error(`HTTP ${response.status}`)
      const blob = await response.blob()
      if (!blob.type.startsWith('image/')) throw new Error('not an image')
      if (blob.size > MAX_INLINE_IMAGE_BYTES) throw new Error('image too large')
      image.setAttribute('src', await blobToDataUrl(blob))
      image.removeAttribute('data-protected-src')
    } catch {
      replaceWithMissingPlaceholder(image)
    }
  }))
}

async function loadKatexCss(): Promise<string> {
  const [cssModule, katexModule] = await Promise.all([
    import('katex/dist/katex.min.css?inline'),
    import('katex'),
  ])
  const css = cssModule.default || ''
  const katex = katexModule as unknown as { version?: string; default?: { version?: string } }
  const version = katex.version || katex.default?.version || ''
  if (!version) return css
  // KaTeX's stylesheet references its fonts relatively, which would resolve to
  // a missing `file://` asset inside the exported document. Point them at the
  // exact KaTeX version this build shipped, so formula metrics stay correct.
  return css.replace(
    /url\(fonts\//g,
    `url(https://cdn.jsdelivr.net/npm/katex@${version}/dist/fonts/`,
  )
}

async function loadHljsCss(): Promise<string> {
  const cssModule = await import('highlight.js/styles/github.css?inline')
  return cssModule.default || ''
}

/**
 * Build the exported document. Browser-only: renders mermaid to SVG, inlines
 * protected images and resolves the active font stacks.
 */
async function buildSessionHtml(options: SessionHtmlExportOptions): Promise<string> {
  const exportedAt = options.exportedAt || new Date().toISOString()
  const { createMermaidCodeRenderer, renderMermaidToSvg } = await import('@/utils/mermaidShared.ts')
  const renderer = createExportRenderer(createMermaidCodeRenderer('mermaid-export'))

  const sections: string[] = []
  for (const message of options.messages) {
    const section = renderExportMessage(message, options.labels, { renderer })
    if (section) sections.push(section)
  }

  const root = document.createElement('div')
  root.innerHTML = sections.join('\n')
  stripExportChrome(root)
  await renderMermaidBlocks(root, renderMermaidToSvg)
  await inlineProtectedImages(root, options.sessionId)

  const bodyHtml = root.innerHTML
  const css = buildExportCss({
    fontFamily: resolveFontStack('--app-font-family', DEFAULT_SANS_STACK),
    fontFamilyMono: resolveFontStack('--app-font-family-mono', DEFAULT_MONO_STACK),
    katexCss: bodyHtml.includes('class="katex') ? await loadKatexCss() : undefined,
    hljsCss: bodyHtml.includes('class="hljs') ? await loadHljsCss() : undefined,
  })

  return buildSessionHtmlDocument({
    title: options.title,
    sessionId: options.sessionId,
    exportedAt,
    labels: options.labels,
    bodyHtml,
    css,
    lang: options.lang,
  })
}

/** Trigger the browser download for an already-built document. */
function downloadHtmlFile(html: string, fileName: string): void {
  const blob = new Blob([html], { type: 'text/html;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = fileName
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  URL.revokeObjectURL(url)
}

/** Build and download in one step. */
export async function exportSessionHtml(options: SessionHtmlExportOptions): Promise<void> {
  const html = await buildSessionHtml(options)
  downloadHtmlFile(html, buildExportFileName(options.title, options.sessionId))
}
