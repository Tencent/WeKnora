/**
 * Source locators point from a cited chunk back into the original file.
 * The backend fills them at ingestion time (see internal/types/source_locator.go);
 * ordinals are 1-based and PDF boxes are fractions of the displayed page with
 * the origin at its top-left corner.
 */
export type SourceLocatorType = 'pdf' | 'docx' | 'slide' | 'sheet' | 'text' | 'time' | 'section'

export type SourceLocator = {
  type: SourceLocatorType | string
  mapping?: string
  partial?: boolean
  source_id?: string
  source_hash?: string
  page?: number
  bbox?: [number, number, number, number] | number[]
  block?: number
  slide?: number
  sheet?: string
  row_start?: number
  row_end?: number
  start?: number
  end?: number
  start_ms?: number
  end_ms?: number
  section?: number
  title?: string
  quote?: string
}

/** What the preview should reveal: structural targets plus text to match. */
export type SourceLocateRequest = {
  /** Locators to reveal, most relevant first. */
  locators: SourceLocator[]
  /** Text to find when no locator narrows it down (or to refine one). */
  quotes: string[]
  /** Changes on every request so re-clicking the same citation re-scrolls. */
  token: number
  /** The answer sentence the citation supports, to narrow a coarse locator. */
  sentence?: string
  /** The cited chunk's text, bounding where a narrowed match may come from. */
  scope?: string
  /** Original Markdown, before stripping markup; code may contain literal tags. */
  sourceMarkdown?: string
  unavailable?: boolean
  /** Neighboring source paragraphs bracketing one embedded OCR image. */
  imageContext?: { before: string; after: string }
  imageDigest?: string
}

// ---------------------------------------------------------------------------
// Normalization keeps letters, digits and numeric symbols, and folds case/width. The
// same projection the backend aligner uses, so Markdown syntax, whitespace and
// punctuation differences between parsed text and the rendered original do not
// break matching.

const LETTER_OR_DIGIT = /[\p{L}\p{N}]/u

export function foldChar(ch: string): string {
  if (!LETTER_OR_DIGIT.test(ch)) return ''
  return ch.normalize('NFKC').toLowerCase()
}

/** Normalize layout differences while preserving numbers and comparisons. */
export function normalizeForMatch(text: string): string {
  return normalizeWithPositions(text).norm
}

export function normalizeWithPositions(text: string): { norm: string; pos: number[] } {
  const chars = Array.from(String(text || ''))
  let norm = ''
  const pos: number[] = []
  let offset = 0
  for (let i = 0; i < chars.length; i++) {
    const ch = chars[i]!
    const value = ch.normalize('NFKC')
    const digit = (v: string | undefined) => !!v && /^\p{N}$/u.test(v.normalize('NFKC'))
    let folded = foldChar(ch)
    if (/^[.,/:+−%‰<>=≤≥≠-]$/u.test(value)) {
      let before = i - 1, after = i + 1
      while (before >= 0 && /^[ \t]$/.test(chars[before]!)) before--
      while (after < chars.length && /^[ \t]$/.test(chars[after]!)) after++
      const prev = digit(chars[before]), next = digit(chars[after])
      const symbol = (/[.,/:]/.test(value) && prev && next) ||
        (/^[+−-]$/.test(value) && next) || (/^[%‰]$/.test(value) && prev) || /^[<>=≤≥≠]$/.test(value)
      if (symbol) folded = value.replace('−', '-')
    }
    norm += folded
    // Offsets throughout this module are UTF-16, including supplementary letters.
    for (let j = 0; j < folded.length; j++) pos.push(offset)
    offset += ch.length
  }
  return { norm, pos }
}

export type NormalizedMatch = { start: number; end: number; score: number }
export type TextMatch = { start: number; end: number }

/** All exact candidates; callers must disambiguate before selecting one. */
export function findNormalizedMatches(haystack: string, quote: string): NormalizedMatch[] {
  let needle = normalizeForMatch(quote)
  if (needle.length < 2 || !haystack) return []
  const search = (key: string) => {
    const matches: NormalizedMatch[] = []
    const first = Array.from(key)[0]!, last = Array.from(key).at(-1)!
    for (let from = 0; from <= haystack.length - key.length;) {
      const at = haystack.indexOf(key, from)
      if (at < 0) break
      const end = at + key.length
      // A quoted number must not be a prefix/suffix of a different number.
      const before = haystack.slice(Math.max(0, at - 2), at), after = haystack.slice(end, end + 2)
      const startsInsideNumber = /\p{N}/u.test(first) && /[\p{N}.,:/+−-]$/u.test(before)
      const endsInsideNumber = /\p{N}/u.test(last) && /^[\p{N}.,:/%‰]/u.test(after)
      if (!startsInsideNumber && !endsInsideNumber) matches.push({ start: at, end, score: 1 })
      from = at + 1
    }
    return matches
  }
  const exact = search(needle)
  if (exact.length) return exact
  // A source list label may have been omitted by a renderer. This is the
  // only tolerated prefix difference; never extend a short fuzzy anchor.
  const withoutLabel = quote.replace(/^\s*[（(][一二三四五六七八九十百\d]+[)）]\s*/u, '')
  if (withoutLabel === quote) return []
  needle = normalizeForMatch(withoutLabel)
  return needle.length >= 8 ? search(needle) : []
}

export function findNormalized(haystack: string, quote: string): NormalizedMatch | null {
  const matches = findNormalizedMatches(haystack, quote)
  return matches.length === 1 ? matches[0]! : null
}

export function findTextMatches(text: string, quote: string): TextMatch[] {
  const { norm, pos } = normalizeWithPositions(text)
  return findNormalizedMatches(norm, quote).map((m) => {
    const start = pos[m.start]!, last = pos[m.end - 1]!
    return { start, end: last + (text.codePointAt(last)! > 0xffff ? 2 : 1) }
  })
}

/** Locate a quote in raw text; returns UTF-16 offsets into `text`. */
export function findInText(text: string, quote: string): { start: number; end: number } | null {
  const hits = findTextMatches(text, quote)
  return hits.length === 1 ? hits[0]! : null
}

// ---------------------------------------------------------------------------
// Picking what to reveal for a citation.

/** Explicit quotations can survive a paraphrased sentence without fuzzy matching. */
export function quotedSourceExcerpt(content: string, sentence: string): string | undefined {
  const source = sourceQuoteText(content)
  const candidates = [...sentence.matchAll(/[“「『"]([^”」』"\n]{8,})[”」』"]/gu)]
    .map(m => m[1]!).filter(q => normalizeForMatch(q).length >= 8 && findInText(source, q))
  return candidates.length === 1 ? candidates[0] : undefined
}

/** Narrow only when the answer contains an exact excerpt of the source. */
export function selectLocatorsForSentence(locators: SourceLocator[], sentence: string): SourceLocator[] {
  const list = Array.isArray(locators) ? locators.filter(Boolean) : []
  sentence = quotedSourceExcerpt(list.map(l => l.quote || '').join('\n'), sentence) || sentence
  const needle = normalizeForMatch(sentence)
  if (list.length <= 1 || needle.length < 4) return list
  const exact = list.filter((loc) => findTextMatches(loc.quote || '', sentence).length > 0)
  return exact.length ? exact : list
}

/** Use an exact excerpt, or retain the complete cited source as context. */
export function selectQuoteForSentence(content: string, sentence: string): string[] {
  const source = sourceQuoteText(content)
  if (!source) return []
  // Refine only using text that actually occurs in this source. A paraphrase
  // still opens its cited chunk; it must not select an unrelated first line.
  sentence = quotedSourceExcerpt(content, sentence) || sentence
  const hit = sentence ? findInText(source, sentence) : null
  return hit ? [source.slice(hit.start, hit.end)] : [source]
}

/** Normalize locator payloads from the API, dropping malformed entries. */
export function parseSourceLocators(raw: unknown): SourceLocator[] {
  if (!Array.isArray(raw)) return []
  return raw.filter((item): item is SourceLocator => {
    if (!item || typeof item !== 'object' || typeof item.type !== 'string') return false
    if (item.type === 'pdf' && (!Number.isSafeInteger(item.page) || item.page < 1)) return false
    return true
  }).map((loc) => {
    if (loc.bbox && !validSourceBox(loc.bbox)) { const { bbox, ...rest } = loc; return rest }
    return loc
  })
}

/** 1-based pages targeted by PDF locators, in first-seen order. */
export function locatorPages(locators: SourceLocator[]): number[] {
  const pages: number[] = []
  for (const loc of locators) {
    if (loc.type === 'pdf' && loc.page && !pages.includes(loc.page)) pages.push(loc.page)
  }
  return pages
}

/** Text fragment URL (`#:~:text=`) that makes browsers scroll to and mark a quote. */
export function textFragmentUrl(url: string, quote: string): string {
  const clean = String(quote || '').replace(/\s+/g, ' ').trim()
  if (!url || !clean) return url
  const words = clean.split(' ')
  let fragment: string
  if (clean.length <= 80) {
    fragment = encodeTextFragment(clean)
  } else {
    // start,end form keeps the URL short for long passages.
    const head = clean.slice(0, 40).trim()
    const tail = clean.slice(-40).trim()
    fragment = words.length > 1 || /[　-鿿]/u.test(clean)
      ? `${encodeTextFragment(head)},${encodeTextFragment(tail)}`
      : encodeTextFragment(head)
  }
  const base = url.split('#')[0]
  return `${base}#:~:text=${fragment}`
}

function encodeTextFragment(text: string): string {
  return encodeURIComponent(text).replace(/-/g, '%2D').replace(/,/g, '%2C').replace(/&/g, '%26')
}

export function validSourceBox(box: unknown): box is [number, number, number, number] {
  return Array.isArray(box) && box.length === 4 && box.every((n) => typeof n === 'number' && Number.isFinite(n) && n >= 0 && n <= 1) && box[0] < box[2] && box[1] < box[3]
}

/** Source text, excluding Markdown destinations and HTML presentation tags. */
export function sourceQuoteText(content: string): string {
  return String(content || '').replace(/!\[[^\]\n]*\]\([^\n)]*\)/g, ' ')
    .replace(/\[([^\]\n]+)\]\([^\n)]*\)/g, '$1')
    .replace(/<\/?[a-zA-Z][^>\n]*>/g, ' ')
    .replace(/&(#x[\da-f]+|#\d+|nbsp|amp|lt|gt|quot|apos);/gi, (raw, entity: string) => {
      const named: Record<string, string> = { nbsp: ' ', amp: '&', lt: '<', gt: '>', quot: '"', apos: "'" }
      if (!entity.startsWith('#')) return named[entity.toLowerCase()] || raw
      const hex = entity[1]?.toLowerCase() === 'x'
      const point = Number.parseInt(entity.slice(hex ? 2 : 1), hex ? 16 : 10)
      return Number.isFinite(point) && point > 0 && point <= 0x10ffff ? String.fromCodePoint(point) : raw
    })
    .trim()
}

/** Locate an embedded image by its exact resource identity in the parent chunk. */
export function sourceImageContext(markdown: string, urls: string[]): SourceLocateRequest['imageContext'] {
  const images = [...markdown.matchAll(/!\[[^\]\n]*\]\(([^\n)]*)\)/g)]
  const selected = images.filter(m => urls.includes(m[1]!))
  if (selected.length !== 1) return undefined
  const match = selected[0]!, index = images.indexOf(match)
  const previous = images[index - 1], next = images[index + 1]
  const prefix = markdown.slice(previous ? previous.index! + previous[0].length : 0, match.index)
  const suffix = markdown.slice(match.index! + match[0].length, next?.index ?? markdown.length)
  const before = sourceQuoteText(prefix).trim().split(/\n\s*\n/).at(-1)?.trim() || ''
  const after = sourceQuoteText(suffix).trim().split(/\n\s*\n/)[0]?.trim() || ''
  return before || after ? { before, after } : undefined
}
