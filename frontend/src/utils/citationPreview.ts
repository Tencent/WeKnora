import {
  createChatMarkdownRenderer,
  renderChatMarkdown,
} from './chatMarkdownRenderer'
import {
  createSafeImage,
  escapeHTML,
  isValidImageURL,
  safeMarkdownToHTML,
  sanitizeMarkdownHTML,
} from './security'

const ESCAPED_HTML_BLOCK_RE =
  /&lt;\/?(?:table|thead|tbody|tr|th|td|div|p|br|ul|ol|li|span|strong|em|b|i|img|figure|figcaption|blockquote|h[1-6])(?:[\s/&]|&gt;)/i

const HTML_ENTITY_RE = /&(?:lt|gt|amp|quot|nbsp|#39|#x27|#\d+|#x[0-9a-f]+);/gi
const MARKDOWN_IMAGE_LINE_RE = /^(\s*)!\[([^\]]*)\]\(([^)\n]+)\)(?:\{[^}]*\})?\s*$/
const GENERIC_IMAGE_ALT_RE = /^(?:image|img|图片|图像|截图|photo|picture)$/i
const FIGURE_CAPTION_RE = /^(?:图|figure|fig\.?)\s*[\d一二三四五六七八九十百千]+(?:[-－–—.．]\s*[\d一二三四五六七八九十百千]+)*\s*\S+/i

const citationPreviewRenderer = createChatMarkdownRenderer({
  imageRenderer: ({ href, title, text }) => createCitationPreviewImage(href, text || '', title || ''),
  isValidImageUrl: isValidImageURL,
})

function cleanCaptionText(value: string | null | undefined): string {
  return String(value || '')
    .replace(/\s+/g, ' ')
    .trim()
}

function normalizeCaptionForCompare(value: string): string {
  return cleanCaptionText(value)
    .replace(/[：:，,。.;；\s]+/g, '')
    .toLowerCase()
}

function resolveFigureCaption(alt: string, title: string): string {
  const candidates = [alt, title].map(cleanCaptionText)
  return candidates.find((candidate) => (
    candidate &&
    !GENERIC_IMAGE_ALT_RE.test(candidate) &&
    FIGURE_CAPTION_RE.test(candidate)
  )) || ''
}

function createCitationPreviewImage(href: string, alt: string, title: string): string {
  const image = createSafeImage(href, alt, title)
  const caption = resolveFigureCaption(alt, title)
  if (!image || !caption) return image

  return [
    '<figure class="citation-preview-figure">',
    image,
    `<figcaption class="citation-preview-figure__caption">${escapeHTML(caption)}</figcaption>`,
    '</figure>',
  ].join('')
}

export function normalizeCitationPreviewFigures(content: string): string {
  if (!content || !content.includes('![')) return content

  const lines = content.split(/\r?\n/)
  const output: string[] = []

  for (let i = 0; i < lines.length; i += 1) {
    const line = lines[i]
    output.push(line)

    const match = line.match(MARKDOWN_IMAGE_LINE_RE)
    if (!match) continue

    const caption = resolveFigureCaption(match[2], '')
    if (!caption) continue

    let duplicateIndex = i + 1
    while (duplicateIndex < lines.length && !lines[duplicateIndex].trim()) {
      duplicateIndex += 1
    }

    if (
      duplicateIndex < lines.length &&
      normalizeCaptionForCompare(lines[duplicateIndex]) === normalizeCaptionForCompare(caption)
    ) {
      i = duplicateIndex
    }
  }

  return output.join('\n')
}

function decodeEntity(entity: string): string {
  const lower = entity.toLowerCase()
  switch (lower) {
    case '&lt;':
      return '<'
    case '&gt;':
      return '>'
    case '&amp;':
      return '&'
    case '&quot;':
      return '"'
    case '&#39;':
    case '&#x27;':
      return "'"
    case '&nbsp;':
      return ' '
    default:
      if (lower.startsWith('&#x')) {
        const codePoint = Number.parseInt(lower.slice(3, -1), 16)
        return Number.isFinite(codePoint) ? String.fromCodePoint(codePoint) : entity
      }
      if (lower.startsWith('&#')) {
        const codePoint = Number.parseInt(lower.slice(2, -1), 10)
        return Number.isFinite(codePoint) ? String.fromCodePoint(codePoint) : entity
      }
      return entity
  }
}

export function decodeEscapedHtmlBlocks(content: string): string {
  if (!ESCAPED_HTML_BLOCK_RE.test(content)) {
    return content
  }

  let decoded = content
  for (let i = 0; i < 3; i += 1) {
    const next = decoded.replace(HTML_ENTITY_RE, decodeEntity)
    if (next === decoded) break
    decoded = next
  }
  return decoded
}

type CitationPreviewRenderOptions = {
  sanitizeHtml?: (html: string) => string
}

export function renderCitationPreviewContent(
  content: string,
  options: CitationPreviewRenderOptions = {},
): string {
  const source = normalizeCitationPreviewFigures(
    decodeEscapedHtmlBlocks(String(content || '').trim()),
  )
  if (!source) return ''

  return renderChatMarkdown(source, {
    renderer: citationPreviewRenderer,
    escapeMarkdown: safeMarkdownToHTML,
    sanitizeHtml: options.sanitizeHtml ?? sanitizeMarkdownHTML,
    streaming: false,
    collapseStandaloneCitations: false,
  })
}
