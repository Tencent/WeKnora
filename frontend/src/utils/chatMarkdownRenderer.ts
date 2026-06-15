import { marked, type Renderer } from 'marked'
import markedKatex from 'marked-katex-extension'
import type { Tokens } from 'marked'

import {
  collapseStandaloneCitationParagraphs,
  extractCitationHtmlPlaceholders,
  joinCitationTagsToPreviousLine,
  preserveCitationTags,
  restoreCitationHtmlPlaceholders,
  restoreCitationTags,
  type CitationKnowledgeRef,
} from './citationMarkdown.ts'

const STREAMING_IMAGE_PLACEHOLDER =
  '<span class="streaming-image-loading"><span class="streaming-image-loading__skeleton"></span></span>'

let markedConfigured = false

export type ImageRendererArgs = {
  href: string
  title: string | null
  text: string
}

export type ChatMarkdownRendererOptions = {
  codeRenderer?: Renderer['code']
  imageRenderer?: (args: ImageRendererArgs) => string
  invalidImageHtml?: (href: string) => string
  isValidImageUrl?: (href: string) => boolean
}

export type RenderChatMarkdownOptions = {
  renderer: Renderer
  escapeMarkdown: (markdown: string) => string
  sanitizeHtml: (html: string) => string
  collapseStandaloneCitations?: boolean
  knowledgeReferences?: CitationKnowledgeRef[] | null
  cachedMermaidSvgHtml?: string | null
  injectCachedMermaidSvg?: (html: string, cachedSvgHtml?: string | null) => string
  prepareMarkdown?: (markdown: string, cachedSvgHtml?: string | null) => string
}

export function configureMarkedForChatMarkdown(): void {
  if (markedConfigured) return
  marked.use({ breaks: true, gfm: true })
  marked.use(markedKatex({ throwOnError: false, nonStandard: true }))
  markedConfigured = true
}

export function preprocessMathDelimiters(rawText: string): string {
  if (!rawText || typeof rawText !== 'string') return ''
  return rawText
    .replace(/\\\[([\s\S]*?)\\\]/g, '$$$$$1$$$$')
    .replace(/\\\(([\s\S]*?)\\\)/g, '$$$1$$')
}

export function replaceIncompleteImageWithPlaceholder(content: string): string {
  if (!content) return ''

  const lastImgStart = content.lastIndexOf('![')
  if (lastImgStart < 0) return content

  const tail = content.slice(lastImgStart)
  const hasImageOpen = tail.startsWith('![')
  const hasBracketClose = tail.includes(']')
  const hasParenOpen = tail.includes('(')
  const hasParenClose = tail.includes(')')
  if (!hasImageOpen) return content

  if (!hasBracketClose || (hasParenOpen && !hasParenClose)) {
    return content.slice(0, lastImgStart) + STREAMING_IMAGE_PLACEHOLDER
  }

  return content
}

export function createChatMarkdownRenderer(options: ChatMarkdownRendererOptions = {}): Renderer {
  const renderer = new marked.Renderer()

  if (options.imageRenderer) {
    renderer.image = ({ href, title, text }: Tokens.Image) => {
      const imageHref = href || ''
      if (options.isValidImageUrl && !options.isValidImageUrl(imageHref)) {
        return options.invalidImageHtml?.(imageHref) ?? ''
      }
      return options.imageRenderer?.({
        href: imageHref,
        title: title || null,
        text: text || '',
      }) ?? ''
    }
  }

  if (options.codeRenderer) {
    renderer.code = options.codeRenderer
  }

  return renderer
}

export function wrapChatMarkdownTables(html: string): string {
  if (!html || !html.includes('<table')) return html
  return html.replace(
    /<table\b[\s\S]*?<\/table>/gi,
    (tableHtml) => `<div class="chat-markdown-table">${tableHtml}</div>`,
  )
}

export function renderChatMarkdown(rawMarkdown: unknown, options: RenderChatMarkdownOptions): string {
  const rawText = typeof rawMarkdown === 'string' ? rawMarkdown : String(rawMarkdown || '')
  if (!rawText.trim()) return ''

  configureMarkedForChatMarkdown()

  const { text: tagSafe, tags } = preserveCitationTags(rawText)
  const imageSafe = replaceIncompleteImageWithPlaceholder(tagSafe)
  const mathSafe = preprocessMathDelimiters(imageSafe)
  const restoredTags = restoreCitationTags(mathSafe, tags)
  const inlineTags = joinCitationTagsToPreviousLine(restoredTags)
  const preparedMarkdown = options.prepareMarkdown
    ? options.prepareMarkdown(inlineTags, options.cachedMermaidSvgHtml)
    : inlineTags
  // Convert <kb>/<web>/wiki tags to HTML placeholders before escapeMarkdown so
  // agent sanitizers (e.g. UUID stripping) cannot damage chunk_id attributes.
  const { content: markdownWithPlaceholders, htmlSnippets } =
    extractCitationHtmlPlaceholders(preparedMarkdown, options.knowledgeReferences)
  const escapedMarkdown = options.escapeMarkdown(markdownWithPlaceholders)
  const html = marked.parse(markdownWithPlaceholders, {
    renderer: options.renderer,
    breaks: true,
    async: false,
  }) as string
  const restoredHtml = restoreCitationHtmlPlaceholders(html, htmlSnippets)
  const citationHtml = options.collapseStandaloneCitations === false
    ? restoredHtml
    : collapseStandaloneCitationParagraphs(restoredHtml)
  const tableWrappedHtml = wrapChatMarkdownTables(citationHtml)
  const sanitized = options.sanitizeHtml(tableWrappedHtml)
  return options.injectCachedMermaidSvg
    ? options.injectCachedMermaidSvg(sanitized, options.cachedMermaidSvgHtml)
    : sanitized
}
