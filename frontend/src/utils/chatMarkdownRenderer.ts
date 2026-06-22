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
  stripIncompleteCitationTag,
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
  /** The source is still growing and may end in ambiguous partial Markdown. */
  streaming?: boolean
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

/**
 * Hide a trailing Markdown horizontal-rule candidate while content is streaming.
 *
 * A model often emits `---` as the beginning of a table delimiter or another
 * structure. At that exact typewriter frame, marked renders it as `<hr>`, then
 * removes it when more characters arrive. A real completed horizontal rule is
 * still rendered because this guard is enabled only for an active stream.
 */
export function stripTrailingStreamingHorizontalRule(content: string): string {
  if (!content) return content
  return content.replace(
    /(^|\n)[ \t]{0,3}(?:(?:-[ \t]*){3,}|(?:\*[ \t]*){3,}|(?:_[ \t]*){3,})$/,
    '$1',
  )
}

function maskMatches(text: string, regex: RegExp): string {
  return text.replace(regex, (match) => match.replace(/[^\n]/g, '\u0000'))
}

function isOdd(text: string, marker: RegExp): boolean {
  const matches = text.match(marker)
  return matches ? matches.length % 2 === 1 : false
}

/**
 * Stabilize dangling inline markers while content is streaming.
 *
 * Two distinct mid-stream artifacts are smoothed out:
 *
 *  - A marker run with **nothing after it yet** (`… 实验**\n5. *`, or a bare
 *    trailing `**`) is ambiguous — it might open bold, italic, or be the start
 *    of the next `**`. Rendering it shows literal `*`/`~` that vanish a frame
 *    later, so it is hidden until real content follows (like the citation/image
 *    guards).
 *  - A marker run **with content after it** but no closer (`**平台地址：…`) is a
 *    genuinely opened emphasis; its closer is appended so marked renders it as
 *    emphasis from the first frame instead of showing raw `**` and then snapping
 *    to bold (and, for a standalone bold line, also gaining the subtitle
 *    margin) — the jump reported on streamed bold lists/headings.
 *
 * Inline/fenced code and leading list bullets are masked so their literal
 * `*`/`` ` `` are never treated as emphasis, and an open fenced block is left
 * untouched.
 */
export function closeDanglingStreamingEmphasis(text: string): string {
  if (!text || !/[*~`]/.test(text)) return text

  // Mask code so markers inside it are never counted; \u0000 keeps offsets.
  let masked = maskMatches(text, /```[\s\S]*?```/g)
  if (isOdd(masked, /```/g)) {
    const open = masked.lastIndexOf('```')
    masked = masked.slice(0, open) + masked.slice(open).replace(/[^\n]/g, '\u0000')
  }
  masked = maskMatches(masked, /`[^`\n]*`/g)
  let appendInlineCode = false
  if (isOdd(masked, /`/g)) {
    appendInlineCode = true
    const open = masked.lastIndexOf('`')
    masked = masked.slice(0, open) + masked.slice(open).replace(/[^\n]/g, '\u0000')
  }

  // Drop a trailing emphasis-marker run with no content after it: ambiguous and
  // not yet renderable. Anchored to the current line so earlier text is intact.
  let working = text
  if (!appendInlineCode) {
    const trailing = masked.match(/[*~]+[ \t]*$/)
    if (trailing) {
      working = text.slice(0, text.length - trailing[0].length)
      masked = masked.slice(0, masked.length - trailing[0].length)
    }
  }

  // Leading list/bullet markers are structure, not emphasis.
  masked = masked.replace(/^(\s*)([*+-])(\s)/gm, (_m, p1, _p2, p3) => `${p1}\u0000${p3}`)

  const needStrike = isOdd(masked, /~~/g)
  const withoutBold = maskMatches(masked, /\*\*/g)
  const needBold = isOdd(masked, /\*\*/g)
  const needItalic = isOdd(withoutBold, /\*/g)

  let closers = ''
  if (appendInlineCode) closers += '`'
  if (needItalic) closers += '*'
  if (needBold) closers += '**'
  if (needStrike) closers += '~~'

  if (!closers) return working

  // A closing emphasis delimiter must not be preceded by whitespace (CommonMark
  // flanking rule), so insert the closers before any trailing whitespace. Bold
  // content with internal spaces (`**XBRL Reporting `) otherwise renders literal
  // (non-bold) on every frame that pauses on a space, flickering bold on/off.
  const trailingWs = working.match(/\s+$/)
  if (!trailingWs) return working + closers
  const cut = working.length - trailingWs[0].length
  return working.slice(0, cut) + closers + working.slice(cut)
}

const FLANKING_BOLD = /(?<!\*)\*\*(?=\S)([^*\n]*?\p{P})\*\*(?=[\p{L}\p{N}])/gu
const FLANKING_STRIKE = /(?<!~)~~(?=\S)([^~\n]*?\p{P})~~(?=[\p{L}\p{N}])/gu
const FLANKING_ITALIC = /(?<![*\p{L}\p{N}])\*(?=\S)([^*\n]*?\p{P})\*(?=[\p{L}\p{N}])/gu

// Opening-delimiter variant of the rule above. CommonMark also refuses to *open*
// emphasis when the run is preceded by a letter/number and immediately followed
// by punctuation, so `知识库中**《手册》**整理` stays literal even though the
// closing `**` is fine. The `(?=\p{P})` guard keeps exponent/glob/math markers
// like `x**2`, `2**3**4`, and `**/*.js` untouched (those are followed by a
// number or slash-as-content, not an emphasis-opening punctuation run).
const FLANKING_BOLD_OPEN = /(?<=[\p{L}\p{N}])\*\*(?=\p{P})([^*\n]+?)\*\*/gu
const FLANKING_STRIKE_OPEN = /(?<=[\p{L}\p{N}])~~(?=\p{P})([^~\n]+?)~~/gu

function repairFlankingEmphasisSegment(segment: string): string {
  return segment
    .replace(FLANKING_BOLD, '<strong>$1</strong>')
    .replace(FLANKING_BOLD_OPEN, '<strong>$1</strong>')
    .replace(FLANKING_STRIKE, '<del>$1</del>')
    .replace(FLANKING_STRIKE_OPEN, '<del>$1</del>')
    .replace(FLANKING_ITALIC, '<em>$1</em>')
}

/**
 * Render emphasis the model clearly intended but CommonMark refuses to parse.
 *
 * CommonMark's flanking rule rejects a closing `**`/`*`/`~~` that is preceded by
 * punctuation and immediately followed by a letter/number, so a very common
 * model pattern like `**XBRL（…语言）**是一种` (and even ASCII `**a)**b`) renders
 * as literal asterisks — both mid-stream and when complete. The mirror case is
 * an *opening* `**`/`~~` preceded by a letter/number and immediately followed by
 * punctuation (`知识库中**《手册》**整理`), which CommonMark also refuses to open.
 * We convert just those blocked patterns into explicit HTML so they bold
 * reliably. Code spans/fences are skipped so their literal markers are untouched.
 */
export function repairFlankingEmphasis(text: string): string {
  if (!text || !/[*~]/.test(text)) return text
  const parts = text.split(/(```[\s\S]*?```|`[^`\n]*`)/g)
  for (let i = 0; i < parts.length; i += 2) {
    parts[i] = repairFlankingEmphasisSegment(parts[i])
  }
  return parts.join('')
}

/**
 * Hide a trailing list/underline marker that has no content after it yet.
 *
 * While streaming, the moment a nested bullet's dash arrives before its text
 * (`1. **AAAAA**\n   - `), marked reads the lone `-` as a setext underline and
 * renders the previous line as a giant `<h2>`, which collapses back to bold text
 * once the bullet content streams in — a large layout swing. An ordered marker
 * with no content (`text\n1. `) and bare setext underlines (`==`/`--`) cause the
 * same fl/flicker. Dropping the dangling marker line defers it by a token until
 * it is unambiguous; a real list/heading still renders once content follows.
 *
 * A trailing line that is only a number (`…\n1`) is the start of an ordered
 * marker before its `.`/`)` arrives; rendering it shows a bare unstyled "1" that
 * disappears a frame later, so it is hidden too. Because the line must be only
 * the number, in-sentence numbers (`值是 1`) are unaffected.
 */
export function stripTrailingStreamingListMarker(text: string): string {
  if (!text) return text
  return text.replace(/(^|\n)[ \t]*(?:[-*+]|[-=]{2,}|\d{1,9}[.)]?)[ \t]*$/, '$1')
}

const TAIL_TEXT_RE = />([^<>]+)</g

/**
 * Soften the most-recently streamed characters with a trailing fade.
 *
 * While the answer streams, the newest tail text is wrapped in a
 * `stream-fade-tail` span so CSS can fade its trailing edge (the newest words
 * are faint and settle to full color as more text arrives — the "tail reveal"
 * look). Runs on the final HTML so the span is never touched by the markdown
 * parser or sanitizer, and only the innermost last text run is wrapped.
 */
export function applyStreamingTailFade(html: string, tailLength = 24): string {
  if (!html) return html

  // Track the last text run that actually has visible characters. Whitespace-only
  // runs (e.g. the `\n` marked emits between `</li>` and `</ol>`) are skipped so
  // they do not win the "last run" and suppress the fade.
  let lastMatch: RegExpExecArray | null = null
  let match: RegExpExecArray | null
  TAIL_TEXT_RE.lastIndex = 0
  while ((match = TAIL_TEXT_RE.exec(html)) !== null) {
    if (match[1].trim()) lastMatch = match
  }
  if (!lastMatch) return html

  const text = lastMatch[1]

  const textStart = lastMatch.index + 1
  const textEnd = textStart + text.length
  const chars = Array.from(text)
  const tailChars = chars.slice(Math.max(0, chars.length - tailLength))
  const tail = tailChars.join('')
  // Fade only the trailing run, keeping any leading whitespace outside the span.
  const tailTrimmed = tail.replace(/^\s+/, '')
  if (!tailTrimmed) return html
  const head = text.slice(0, text.length - tailTrimmed.length)
  const wrapped = `${head}<span class="stream-fade-tail">${tailTrimmed}</span>`
  return html.slice(0, textStart) + wrapped + html.slice(textEnd)
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

const STANDALONE_STRONG_PARAGRAPH_RE =
  /<p>\s*<strong>((?:(?!<\/strong>)[\s\S])*?)<\/strong>\s*<\/p>/g

/**
 * Tag paragraphs whose entire content is a single bold run (e.g. a model that
 * emits `**小节标题：**` instead of a real heading) so they can be styled as a
 * subtitle deterministically.
 *
 * This replaces the CSS `p:has(> strong:only-child)` heuristic, which was
 * streaming-unstable: CSS `:only-child` ignores text nodes, so a normal
 * paragraph that momentarily contained exactly one completed `**bold**` run
 * (surrounded by plenty of body text) matched it and gained a large top margin,
 * then lost it once a second bold run finished — a visible spacing jump
 * mid-stream. Matching the whole paragraph content here only fires when the
 * bold run truly is the entire paragraph.
 */
export function markStandaloneStrongParagraphs(html: string): string {
  if (!html || !html.includes('<strong>')) return html
  return html.replace(
    STANDALONE_STRONG_PARAGRAPH_RE,
    (match: string, inner: string, offset: number, full: string) => {
      // A `<p>` that is a list item's content is list text, not a standalone
      // subtitle. Marking it adds a large margin that shifts sibling items when
      // a streamed list flips from tight to loose, so leave list paragraphs to
      // the list's own (zero) margins.
      if (/<li>\s*$/.test(full.slice(0, offset))) return match
      return `<p class="md-strong-title"><strong>${inner}</strong></p>`
    },
  )
}

export function renderChatMarkdown(rawMarkdown: unknown, options: RenderChatMarkdownOptions): string {
  const rawText = typeof rawMarkdown === 'string' ? rawMarkdown : String(rawMarkdown || '')
  if (!rawText.trim()) return ''

  configureMarkedForChatMarkdown()

  const streamingSafeText = options.streaming
    ? stripTrailingStreamingListMarker(stripTrailingStreamingHorizontalRule(rawText))
    : rawText
  const citationSafeText = stripIncompleteCitationTag(streamingSafeText)
  const { text: tagSafe, tags } = preserveCitationTags(citationSafeText)
  const imageSafe = replaceIncompleteImageWithPlaceholder(tagSafe)
  const mathSafe = preprocessMathDelimiters(imageSafe)
  const restoredTags = restoreCitationTags(mathSafe, tags)
  const inlineTags = joinCitationTagsToPreviousLine(restoredTags)
  // Run the list-marker guard again after emphasis balancing: closing/hiding a
  // dangling `*`/`**` (the first chars of the next bullet's bold) can re-expose a
  // bare `   - `, which would otherwise flash an empty nested item under the
  // previous line right as the next item starts streaming.
  const balancedInline = options.streaming
    ? stripTrailingStreamingListMarker(closeDanglingStreamingEmphasis(inlineTags))
    : inlineTags
  const preparedMarkdown = options.prepareMarkdown
    ? options.prepareMarkdown(balancedInline, options.cachedMermaidSvgHtml)
    : balancedInline
  const flankingSafeMarkdown = repairFlankingEmphasis(preparedMarkdown)
  // Convert <kb>/<web>/wiki tags to HTML placeholders before escapeMarkdown so
  // agent sanitizers (e.g. UUID stripping) cannot damage chunk_id attributes.
  const { content: markdownWithPlaceholders, htmlSnippets } =
    extractCitationHtmlPlaceholders(flankingSafeMarkdown, options.knowledgeReferences)
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
  const strongTitleHtml = markStandaloneStrongParagraphs(tableWrappedHtml)
  const sanitized = options.sanitizeHtml(strongTitleHtml)
  const withMermaid = options.injectCachedMermaidSvg
    ? options.injectCachedMermaidSvg(sanitized, options.cachedMermaidSvgHtml)
    : sanitized
  return options.streaming ? applyStreamingTailFade(withMermaid) : withMermaid
}
