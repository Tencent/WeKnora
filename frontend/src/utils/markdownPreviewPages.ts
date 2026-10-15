import type { Token, Tokens } from 'marked'
import { createPreviewMarkdownParser, preprocessMathDelimiters } from './documentPreviewMarkdownParser.ts'

// Keep the current page, repeated table/fence context and retained link
// definitions within a 256 KiB source budget before HTML expansion.
export const MARKDOWN_PAGE_BYTES = 224 * 1024
export const MARKDOWN_CONTEXT_BYTES = 16 * 1024
export const MARKDOWN_DEFINITIONS_BYTES = 16 * 1024
export const MARKDOWN_PAGE_TOKENS = 2_000
const SCAN_CHUNK_BYTES = 64 * 1024

export type MarkdownPreviewPage = {
  start: number
  end: number
  prefix: string
  suffix: string
  plain: boolean
}

export type MarkdownPreviewIndex = {
  pages: MarkdownPreviewPage[]
  definitions: string
}

export type MarkdownPreviewPageContent = {
  source: string
  plain: boolean
}

function tokenCount(tokens: Token[]): number {
  const pending: Token[] = [...tokens]
  let count = 0
  while (pending.length && count <= MARKDOWN_PAGE_TOKENS) {
    const token = pending.pop()!
    count++
    if (token.type === 'html') count += (token.raw.match(/<[a-z][^>]*>/gi) || []).length
    if (token.type === 'table') {
      const table = token as Tokens.Table
      count += table.header.length + table.rows.length
      for (const cell of table.header) pending.push(...cell.tokens)
      for (const row of table.rows) {
        count += row.length
        for (const cell of row) pending.push(...cell.tokens)
      }
    } else {
      if ('tokens' in token && Array.isArray(token.tokens)) pending.push(...token.tokens)
      if ('items' in token && Array.isArray(token.items)) pending.push(...token.items)
    }
  }
  return count
}

type SourceLine = {
  start: number
  end: number
  text: string
  prefix: string
  suffix: string
  plain?: boolean
}

const parser = createPreviewMarkdownParser()
const encoder = new TextEncoder()
const decoder = new TextDecoder()

function sourceTokenCount(source: string): number {
  try {
    return tokenCount(parser.lexer(preprocessMathDelimiters(source)))
  } catch {
    return MARKDOWN_PAGE_TOKENS + 1
  }
}

function isTableRow(line: string): boolean {
  const trimmed = line.trim()
  return trimmed.includes('|') && !/^(```|~~~)/.test(trimmed)
}

function isTableDelimiter(line: string): boolean {
  const cells = line.trim().replace(/^\||\|$/g, '').split('|')
  return cells.length > 0 && cells.every(cell => /^\s*:?-{3,}:?\s*$/.test(cell))
}

function definitionLine(line: string): boolean {
  return /^ {0,3}\[[^\]\n]+\]:\s*\S/.test(line)
}

function fenceStart(line: string): { marker: string; close: string } | null {
  const match = /^ {0,3}(`{3,}|~{3,})/.exec(line)
  if (!match) return null
  return { marker: match[1]!, close: `${match[1]![0]!.repeat(match[1]!.length)}\n` }
}

function closesFence(line: string, marker: string): boolean {
  const char = marker[0]!
  return new RegExp(`^ {0,3}${char === '`' ? '`' : '~'}{${marker.length},}\\s*$`).test(line.trimEnd())
}

function plainPage(start: number, end: number, prefix = '', suffix = ''): MarkdownPreviewPage {
  return { start, end, prefix, suffix, plain: true }
}

async function splitPlainRange(blob: Blob, start: number, end: number, prefix = ''): Promise<MarkdownPreviewPage[]> {
  const result: MarkdownPreviewPage[] = []
  while (end - start > MARKDOWN_PAGE_BYTES) {
    let boundary = start + MARKDOWN_PAGE_BYTES
    // UTF-8 continuation bytes cannot begin an independently decoded page.
    // Moving back at most three bytes keeps every split on a code-point edge.
    for (let i = 0; i < 3 && boundary > start; i++) {
      const byte = new Uint8Array(await blob.slice(boundary, boundary + 1).arrayBuffer())[0]
      if (byte === undefined || (byte & 0xc0) !== 0x80) break
      boundary--
    }
    if (boundary === start) boundary = start + MARKDOWN_PAGE_BYTES
    result.push(plainPage(start, boundary, prefix))
    start = boundary
  }
  if (start < end) result.push(plainPage(start, end, prefix))
  return result
}

/** Build a byte-range index without retaining or lexing the complete document. */
export async function indexMarkdownBlob(blob: Blob): Promise<MarkdownPreviewIndex> {
  const pages: MarkdownPreviewPage[] = []
  let definitions = ''
  let lines: SourceLine[] = []
  let bytes = 0

  const emitLines = (items: SourceLine[]) => {
    if (!items.length) return
    const prefix = items[0]!.prefix
    const suffix = items[items.length - 1]!.suffix
    const source = prefix + items.map(item => item.text).join('') + suffix
    if (items.some(item => item.plain)) {
      pages.push(plainPage(items[0]!.start, items[items.length - 1]!.end))
      return
    }
    if (sourceTokenCount(source) <= MARKDOWN_PAGE_TOKENS) {
      pages.push({ start: items[0]!.start, end: items[items.length - 1]!.end, prefix, suffix, plain: false })
      return
    }
    if (items.length === 1) {
      pages.push(plainPage(items[0]!.start, items[0]!.end, prefix, suffix))
      return
    }
    const middle = Math.ceil(items.length / 2)
    emitLines(items.slice(0, middle))
    emitLines(items.slice(middle))
  }

  const flush = () => {
    emitLines(lines)
    lines = []
    bytes = 0
  }

  const addLine = (line: SourceLine) => {
    const contextBytes = encoder.encode(line.prefix).byteLength + encoder.encode(line.suffix).byteLength
    const lineBytes = line.end - line.start
    if (lineBytes + contextBytes > MARKDOWN_PAGE_BYTES + MARKDOWN_CONTEXT_BYTES) {
      flush()
      pages.push(plainPage(line.start, line.end, line.prefix, line.suffix))
      return
    }
    if (lines.length && bytes + lineBytes > MARKDOWN_PAGE_BYTES) flush()
    lines.push(line)
    bytes += lineBytes
  }

  let pending: Omit<SourceLine, 'prefix' | 'suffix'> | null = null
  let tableHeader = ''
  let fence: { marker: string; opener: string; close: string; plain: boolean } | null = null

  const addStructuredLine = (line: Omit<SourceLine, 'prefix' | 'suffix'>, prefix = '', suffix = '', plain = false) => {
    if (definitionLine(line.text)) {
      const next = definitions + line.text
      if (encoder.encode(next).byteLength <= MARKDOWN_DEFINITIONS_BYTES) definitions = next
    }
    addLine({ ...line, prefix, suffix, plain })
  }

  const processOutsideFence = (line: Omit<SourceLine, 'prefix' | 'suffix'>) => {
    if (tableHeader) {
      if (isTableRow(line.text) && line.text.trim()) {
        addStructuredLine(line, tableHeader)
        return
      }
      tableHeader = ''
    }
    if (pending) {
      if (isTableRow(pending.text) && isTableDelimiter(line.text)) {
        const header = pending.text + line.text
        if (encoder.encode(header).byteLength <= MARKDOWN_CONTEXT_BYTES) {
          addStructuredLine(pending)
          addStructuredLine(line)
          tableHeader = header
        } else {
          addStructuredLine(pending)
          addStructuredLine(line)
        }
        pending = null
        return
      }
      addStructuredLine(pending)
    }
    pending = line
  }

  const processLine = (line: Omit<SourceLine, 'prefix' | 'suffix'>) => {
    if (fence) {
      const closing = closesFence(line.text, fence.marker)
      addStructuredLine(line, fence.opener, closing ? '' : fence.close, fence.plain)
      if (closing) fence = null
      return
    }
    const opening = fenceStart(line.text)
    if (opening) {
      if (pending) { addStructuredLine(pending); pending = null }
      tableHeader = ''
      const plain = encoder.encode(line.text + opening.close).byteLength > MARKDOWN_CONTEXT_BYTES
      fence = { marker: opening.marker, opener: plain ? '' : line.text, close: plain ? '' : opening.close, plain }
      addStructuredLine(line, '', fence.close, plain)
      return
    }
    processOutsideFence(line)
  }

  let lineStart = 0
  for (let chunkStart = 0; chunkStart < blob.size; chunkStart += SCAN_CHUNK_BYTES) {
    const chunkEnd = Math.min(blob.size, chunkStart + SCAN_CHUNK_BYTES)
    const chunk = new Uint8Array(await blob.slice(chunkStart, chunkEnd).arrayBuffer())
    for (let i = 0; i < chunk.length; i++) {
      if (chunk[i] !== 0x0a) continue
      const end = chunkStart + i + 1
      if (end - lineStart > MARKDOWN_PAGE_BYTES) {
        if (pending) { addStructuredLine(pending); pending = null }
        flush()
        pages.push(...await splitPlainRange(blob, lineStart, end, tableHeader))
      } else {
        const text = lineStart >= chunkStart
          ? decoder.decode(chunk.subarray(lineStart - chunkStart, i + 1))
          : await blob.slice(lineStart, end).text()
        processLine({ start: lineStart, end, text })
      }
      lineStart = end
    }
  }
  if (lineStart < blob.size) {
    const end = blob.size
    if (end - lineStart > MARKDOWN_PAGE_BYTES) {
      if (pending) { addStructuredLine(pending); pending = null }
      flush()
      pages.push(...await splitPlainRange(blob, lineStart, end, tableHeader))
    } else {
      processLine({ start: lineStart, end, text: await blob.slice(lineStart, end).text() })
    }
  }
  if (pending) addStructuredLine(pending)
  flush()
  if (!pages.length) pages.push({ start: 0, end: 0, prefix: '', suffix: '', plain: false })
  return { pages, definitions }
}

export async function readMarkdownPage(
  blob: Blob,
  index: MarkdownPreviewIndex,
  pageIndex: number,
): Promise<MarkdownPreviewPageContent> {
  const page = index.pages[pageIndex]
  if (!page) throw new RangeError(`Markdown page ${pageIndex + 1} is out of range`)
  const raw = await blob.slice(page.start, page.end).text()
  const definitions = page.plain || index.pages.length === 1 || !index.definitions ? '' : `\n${index.definitions}`
  return { source: page.prefix + raw + page.suffix + definitions, plain: page.plain }
}

/** Reject ambiguous citations across raw pages instead of matching repeated headers. */
export async function findMarkdownPage(blob: Blob, pages: MarkdownPreviewPage[], quote: string): Promise<number> {
  const needle = quote.trim()
  if (!needle) return -1
  let found = -1
  for (let i = 0; i < pages.length; i++) {
    const page = pages[i]!
    if (!(await blob.slice(page.start, page.end).text()).includes(needle)) continue
    if (found !== -1) return -1
    found = i
  }
  return found
}
