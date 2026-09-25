/**
 * Source locators point from a cited chunk back into the original file.
 * The backend fills them at ingestion time (see internal/types/source_locator.go);
 * ordinals are 1-based and PDF boxes are fractions of the displayed page with
 * the origin at its top-left corner.
 */
export type SourceLocatorType = 'pdf' | 'docx' | 'slide' | 'sheet' | 'text' | 'time' | 'section'

export type SourceLocator = {
  type: SourceLocatorType | string
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
}

// ---------------------------------------------------------------------------
// Normalization: letters and digits only, lower-cased, full-width folded. The
// same projection the backend aligner uses, so Markdown syntax, whitespace and
// punctuation differences between parsed text and the rendered original do not
// break matching.

const LETTER_OR_DIGIT = /[\p{L}\p{N}]/u

export function foldChar(ch: string): string {
  if (!LETTER_OR_DIGIT.test(ch)) return ''
  return ch.normalize('NFKC').toLowerCase()
}

export function normalizeForMatch(text: string): string {
  let out = ''
  for (const ch of String(text || '')) out += foldChar(ch)
  return out
}

/**
 * Normalized projection of `text` with, for each normalized character, the
 * UTF-16 offset in `text` of the character it came from.
 */
export function normalizeWithPositions(text: string): { norm: string; pos: number[] } {
  let norm = ''
  const pos: number[] = []
  let i = 0
  for (const ch of String(text || '')) {
    const folded = foldChar(ch)
    for (const f of folded) {
      norm += f
      pos.push(i)
    }
    i += ch.length
  }
  return { norm, pos }
}

// ---------------------------------------------------------------------------
// Fuzzy location of a quote inside a normalized haystack.

const KEY_LEN = 24

export type NormalizedMatch = { start: number; end: number; score: number }

/**
 * Find `quote` (raw text) inside `haystack` (already normalized). Tries the
 * whole quote, then anchors from its head, middle and tail, and extends the
 * match to the quote's length. Returns normalized offsets or null.
 */
export function findNormalized(haystack: string, quote: string): NormalizedMatch | null {
  const needle = normalizeForMatch(quote)
  if (needle.length < 2 || !haystack) return null
  const whole = haystack.indexOf(needle)
  if (whole >= 0) return { start: whole, end: whole + needle.length, score: 1 }
  if (needle.length <= KEY_LEN) return null

  const anchors: Array<{ offset: number; key: string }> = [
    { offset: 0, key: needle.slice(0, KEY_LEN) },
    { offset: Math.floor(needle.length / 2), key: needle.slice(Math.floor(needle.length / 2), Math.floor(needle.length / 2) + KEY_LEN) },
    { offset: needle.length - KEY_LEN, key: needle.slice(-KEY_LEN) },
  ]
  let best: NormalizedMatch | null = null
  for (const anchor of anchors) {
    let from = 0
    // Every occurrence of the anchor is a candidate; score by how much of
    // the quote agrees around it.
    for (let guard = 0; guard < 50; guard++) {
      const at = haystack.indexOf(anchor.key, from)
      if (at < 0) break
      from = at + 1
      const start = Math.max(0, at - anchor.offset)
      const end = Math.min(haystack.length, start + needle.length)
      const score = ngramOverlap(needle, haystack.slice(start, end))
      if (!best || score > best.score) best = { start, end, score }
    }
  }
  return best && best.score >= 0.5 ? best : null
}

/** Share of the character bigrams of `a` that also occur in `b`. */
export function ngramOverlap(a: string, b: string): number {
  if (a.length < 2 || b.length < 2) return a && b && a === b ? 1 : 0
  const counts = new Map<string, number>()
  for (let i = 0; i + 1 < b.length; i++) {
    const g = b.slice(i, i + 2)
    counts.set(g, (counts.get(g) || 0) + 1)
  }
  let hit = 0
  for (let i = 0; i + 1 < a.length; i++) {
    const g = a.slice(i, i + 2)
    const n = counts.get(g) || 0
    if (n > 0) {
      hit++
      counts.set(g, n - 1)
    }
  }
  return hit / (a.length - 1)
}

/** Locate a quote in raw text; returns UTF-16 offsets into `text`. */
export function findInText(text: string, quote: string): { start: number; end: number } | null {
  const { norm, pos } = normalizeWithPositions(text)
  const m = findNormalized(norm, quote)
  if (!m || !pos.length) return null
  const start = pos[m.start]
  const lastIdx = Math.min(m.end, pos.length) - 1
  const last = pos[lastIdx]
  // Extend to cover the whole last character (surrogate pairs included).
  const end = last + (text.codePointAt(last)! > 0xffff ? 2 : 1)
  return { start, end }
}

// ---------------------------------------------------------------------------
// Picking what to reveal for a citation.

/** Split text into sentence-sized pieces for alignment. */
export function splitSentences(text: string): string[] {
  return String(text || '')
    .split(/(?<=[。！？!?；;])\s*|\n+/u)
    .map((s) => s.trim())
    .filter((s) => normalizeForMatch(s).length >= 4)
}

const ALIGN_THRESHOLD = 0.35

/**
 * Narrow a chunk's locators to the ones that support the cited sentence. The
 * answer paraphrases the source, so each locator's quote is scored by how
 * many of the sentence's character bigrams it contains; locators well below
 * the best are dropped. When nothing scores, every locator is kept.
 */
export function selectLocatorsForSentence(locators: SourceLocator[], sentence: string): SourceLocator[] {
  const list = Array.isArray(locators) ? locators.filter(Boolean) : []
  const needle = normalizeForMatch(sentence)
  if (list.length <= 1 || needle.length < 4) return list
  const scored = list.map((loc) => ({ loc, score: ngramOverlap(needle, normalizeForMatch(loc.quote || '')) }))
  const best = Math.max(...scored.map((s) => s.score))
  if (best < ALIGN_THRESHOLD) return list
  return scored.filter((s) => s.score >= best * 0.8).map((s) => s.loc)
}

/**
 * Indices of the texts that best support `sentence`: every text within 80% of
 * the best once it clears the alignment threshold, or else the single best
 * when it clearly stands out (table rows share few bigrams with prose).
 * Empty when nothing aligns, so the caller keeps the whole region.
 */
export function pickBestTexts(texts: string[], sentence: string): number[] {
  const needle = normalizeForMatch(sentence || '')
  if (needle.length < 4 || texts.length < 2) return []
  const scores = texts.map((t) => ngramOverlap(needle, normalizeForMatch(t)))
  const best = Math.max(...scores)
  if (best >= ALIGN_THRESHOLD) return scores.flatMap((s, i) => (s >= best * 0.8 ? [i] : []))
  const top = scores.indexOf(best)
  const second = Math.max(0, ...scores.filter((_, i) => i !== top))
  return best >= 0.2 && second <= best * 0.5 ? [top] : []
}

/**
 * Code point ranges of the lines within `spans` of `text` that best support
 * `sentence`, or empty when none aligns. Offsets are code points, the unit
 * text locators use.
 */
export function narrowTextLines(text: string, spans: Array<[number, number]>, sentence: string): Array<[number, number]> {
  const chars = Array.from(text || '')
  const lines: Array<{ text: string; start: number; end: number }> = []
  for (const [from, to] of spans) {
    const stop = Math.min(to, chars.length)
    let lineStart = Math.max(0, from)
    for (let i = lineStart; i <= stop; i++) {
      if (i < stop && chars[i] !== '\n') continue
      const line = chars.slice(lineStart, i).join('')
      if (line.trim()) lines.push({ text: line, start: lineStart, end: i })
      lineStart = i + 1
    }
  }
  return pickBestTexts(
    lines.map((l) => l.text),
    sentence,
  ).map((i) => [lines[i].start, lines[i].end])
}

/**
 * The piece of a chunk's text that best supports the cited sentence, used as
 * the quote to search when there are no locators. Falls back to the chunk's
 * opening when the sentence matches nothing.
 */
export function selectQuoteForSentence(content: string, sentence: string): string[] {
  const pieces = splitSentences(content)
  if (!pieces.length) return content ? [content.slice(0, 200)] : []
  const needle = normalizeForMatch(sentence)
  if (needle.length >= 4) {
    let best = { piece: '', score: 0 }
    for (const piece of pieces) {
      const score = ngramOverlap(needle, normalizeForMatch(piece))
      if (score > best.score) best = { piece, score }
    }
    if (best.score >= ALIGN_THRESHOLD) return [best.piece, pieces[0]]
  }
  return [pieces[0], pieces.slice(0, 3).join('')]
}

/** Normalize locator payloads from the API, dropping malformed entries. */
export function parseSourceLocators(raw: unknown): SourceLocator[] {
  if (!Array.isArray(raw)) return []
  return raw.filter((item): item is SourceLocator => !!item && typeof item === 'object' && typeof (item as SourceLocator).type === 'string')
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
