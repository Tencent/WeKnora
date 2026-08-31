/** Pure projections from a tool's canonical value to model-facing text. */

/** One figure taken from chunk `image_info` or from Markdown / HTML in the passage. */
export interface ImageRef {
  url: string
  caption: string
  ocr_text: string
}

/** Clip text to a character budget, reporting whether anything was dropped. */
export function clip(text: string, maxChars: number): { text: string, truncated: boolean } {
  const normalized = text.replace(/\r\n/g, '\n').trim()
  if (normalized.length <= maxChars) return { text: normalized, truncated: false }
  return { text: `${normalized.slice(0, maxChars).trimEnd()}…`, truncated: true }
}

/**
 * Drop a Markdown image that `clip` cut in half. A truncated `![alt](https://…`
 * is worse than omitting the figure: the full URL still lives on `images`.
 */
export function dropTruncatedImageTail(text: string): string {
  return text.replace(/!\[[^\]]*$/, '').replace(/!\[[^\]]*\]\([^)\n]*$/, '').trimEnd()
}

/** Clip a passage, then strip a broken trailing image so the URL is never half-emitted. */
export function clipPassage(text: string, maxChars: number): { text: string, truncated: boolean } {
  const clipped = clip(text, maxChars)
  const body = dropTruncatedImageTail(clipped.text)
  if (!clipped.truncated) return { text: body, truncated: false }
  return { text: body.endsWith('…') ? body : `${body}…`, truncated: true }
}

/** Human-readable score, tolerant of a backend that omits it. */
export function formatScore(score: number): string {
  return Number.isFinite(score) ? score.toFixed(3) : 'n/a'
}

/**
 * Name a retrieval scope without spending the model's context on it. A
 * deployment can hold dozens of knowledge bases, and spelling out every id on
 * every search costs far more than the count is worth; the ids stay in the
 * tool's canonical value for anything that needs them.
 */
export function describeScope(ids: string[]): string {
  if (ids.length === 0) return '(deployment default)'
  if (ids.length <= 3) return ids.join(', ')
  return `${ids.length} knowledge bases`
}

function asImage(url: string, caption: string, ocrText: string): ImageRef | undefined {
  const trimmed = url.trim()
  if (trimmed === '') return undefined
  return { url: trimmed, caption: caption.trim(), ocr_text: ocrText.trim() }
}

/**
 * Parse WeKnora's `image_info` field. The API serializes it as a JSON string
 * of `{url, caption, ocr_text}` objects; a decoded array is accepted as well
 * so a proxy that already parsed the field does not drop the figures.
 */
export function parseImageInfo(raw: unknown): ImageRef[] {
  let records: unknown[] = []
  if (typeof raw === 'string' && raw.trim() !== '') {
    try {
      const parsed: unknown = JSON.parse(raw)
      if (Array.isArray(parsed)) records = parsed
    } catch {
      return []
    }
  } else if (Array.isArray(raw)) {
    records = raw
  }
  const out: ImageRef[] = []
  for (const entry of records) {
    if (typeof entry !== 'object' || entry === null) continue
    const record = entry as Record<string, unknown>
    const image = asImage(
      typeof record.url === 'string' ? record.url : '',
      typeof record.caption === 'string' ? record.caption : '',
      typeof record.ocr_text === 'string' ? record.ocr_text : '',
    )
    if (image !== undefined) out.push(image)
  }
  return out
}

const MARKDOWN_IMAGE = /!\[([^\]]*)\]\(([^)]+)\)/g
const HTML_IMAGE = /<img\b[^>]*\ssrc\s*=\s*["']([^"']+)["'][^>]*>/gi

/** Collect figures already written into a passage as Markdown or HTML. */
export function collectContentImages(text: string): ImageRef[] {
  const out: ImageRef[] = []
  if (text === '') return out
  for (const match of text.matchAll(MARKDOWN_IMAGE)) {
    const image = asImage(match[2] ?? '', match[1] ?? '', '')
    if (image !== undefined) out.push(image)
  }
  for (const match of text.matchAll(HTML_IMAGE)) {
    const image = asImage(match[1] ?? '', '', '')
    if (image !== undefined) out.push(image)
  }
  return out
}

/** Deduplicate by URL, keeping the first non-empty caption and OCR text. */
export function mergeImages(...groups: ImageRef[][]): ImageRef[] {
  const byUrl = new Map<string, ImageRef>()
  for (const group of groups) {
    for (const image of group) {
      const existing = byUrl.get(image.url)
      if (existing === undefined) {
        byUrl.set(image.url, { url: image.url, caption: image.caption, ocr_text: image.ocr_text })
        continue
      }
      byUrl.set(image.url, {
        url: image.url,
        caption: existing.caption !== '' ? existing.caption : image.caption,
        ocr_text: existing.ocr_text !== '' ? existing.ocr_text : image.ocr_text,
      })
    }
  }
  return [...byUrl.values()]
}

/** Figures on one WeKnora record: structured `image_info` plus any inline markup. */
export function collectImages(record: { content?: unknown, image_info?: unknown }): ImageRef[] {
  const content = typeof record.content === 'string' ? record.content : ''
  return mergeImages(parseImageInfo(record.image_info), collectContentImages(content))
}

function escapeAlt(value: string): string {
  return value.replace(/\\/g, '\\\\').replace(/\[/g, '\\[').replace(/\]/g, '\\]')
}

/** Answer-ready Markdown for one figure, matching WeKnora's chat image protocol. */
export function formatImageMarkdown(image: ImageRef): string {
  const alt = escapeAlt(image.caption.replace(/\s+/g, ' ') || 'image')
  const block = `![${alt}](${image.url})`
  const notes = imageNotes(image)
  return notes === '' ? block : `${block}\n\n${notes}`
}

/** Join several figures with a blank line between them. */
export function formatImagesMarkdown(images: ImageRef[]): string {
  if (images.length === 0) return ''
  return images.map(image => formatImageMarkdown(image)).join('\n\n')
}

/** Figures whose URL is not already present in `text`. */
export function imagesMissingFrom(text: string, images: ImageRef[]): ImageRef[] {
  return images.filter(image => !text.includes(image.url))
}

function imageNotes(image: ImageRef): string {
  const notes: string[] = []
  if (image.caption !== '') notes.push(`**Image caption:** ${image.caption}`)
  if (image.ocr_text !== '') notes.push(`**Image text (OCR):** ${image.ocr_text}`)
  if (notes.length === 0) return ''
  return `> ${notes.join('\n\n').replace(/\n/g, '\n> ')}`
}

/**
 * Append figures the passage does not already contain, and attach caption /
 * OCR notes to figures that are already inline. Inline Markdown that survived
 * clipping stays where it is; clipped-away figures are restored after the
 * text so their URLs are never truncated.
 */
export function appendImages(text: string, images: ImageRef[]): string {
  const missing = imagesMissingFrom(text, images)
  let out = text
  if (missing.length > 0) {
    const block = formatImagesMarkdown(missing)
    out = out === '' ? block : `${out}\n\n${block}`
  }
  const extraNotes = images
    .filter(image => !missing.includes(image))
    .map(image => imageNotes(image))
    .filter(note => note !== '' && !out.includes(note))
  if (extraNotes.length === 0) return out
  return `${out}\n\n${extraNotes.join('\n\n')}`
}
