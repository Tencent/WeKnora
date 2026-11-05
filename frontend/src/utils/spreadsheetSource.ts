import {
  findInText,
  normalizeForMatch,
  sourceQuoteText,
  type SourceLocateRequest,
  type SourceLocator,
} from './sourceLocator'

export type SpreadsheetSourceRow = { number: number; cells: string[] }

// Backend fullQuote folds whitespace (including <br />), and joinQuote
// separates consecutive rows with a space. Keep escaped cell data distinct
// from structural delimiters in both stored and newline-separated quotes.
function tableText(text: string): string {
  return text
    .normalize('NFC')
    .replace(/\s+/gu, ' ')
    .trim()
    .replace(/\\[\\|]| *\| */g, (match) => match.trim())
}

function quoteText(text: string): string {
  return tableText(text.replace(/<br\s*\/?\s*>/gi, ' '))
}

function rowText(cells: string[]): string {
  return (
    '| ' + cells.map((cell) => cell.replace(/\\/g, '\\\\').replace(/\|/g, '\\|')).join(' | ') + ' |'
  )
}

function matchTableRows(
  rows: SpreadsheetSourceRow[],
  quote: string,
  csv: boolean,
  sourceMarkdown: string,
): SpreadsheetSourceRow[] {
  // Explicit line breaks between Markdown rows still carry row identity.
  const lines = quote.split(/(?<=\|)[ \t]*\r?\n(?=[ \t]*\|)/)
  const separator = lines.length > 1 ? '\n' : ''
  const needle = lines.map(quoteText).join(separator)
  const offsets = new Map<number, number>()
  const ends = new Map<number, number>()
  let text = '',
    headerRule = ''
  for (const [index, row] of rows.entries()) {
    if (index) text += separator
    offsets.set(text.length, index)
    text += tableText(rowText(row.cells))
    ends.set(text.length, index + 1)
    if (csv && row.number === 1) {
      const delimiter = tableText(rowText(row.cells.map(() => '---')))
      if (needle.startsWith(text + separator + delimiter)) {
        headerRule = delimiter
        text += separator + delimiter
        ends.set(text.length, index + 1)
      }
    }
  }
  let found: SpreadsheetSourceRow[] = []
  for (let at = text.indexOf(needle); at >= 0; at = text.indexOf(needle, at + 1)) {
    const start = offsets.get(at)
    const end = ends.get(at + needle.length)
    if (start === undefined || end === undefined) continue
    if (rows[end - 1]!.number - rows[start]!.number !== end - start - 1) continue
    if (found.length) return []
    found = rows.slice(start, end)
  }
  // In folded quotes an empty cell and the boundary between two rows both
  // appear as adjacent pipes. Require the original chunk's physical lines
  // when a multi-row mapping would otherwise depend on that distinction.
  if (
    !separator &&
    rows.length > 1 &&
    found.some((row) => row.cells.some((cell) => !cell.trim()))
  ) {
    const original = sourceMarkdown.split(/\r?\n/).map(quoteText)
    const folded = quoteText(sourceMarkdown)
    const first = folded.indexOf(needle)
    if (first < 0 || folded.indexOf(needle, first + 1) >= 0) return []
    const expected = found.map((row) => tableText(rowText(row.cells)))
    if (headerRule && found[0]?.number === 1) expected.splice(1, 0, headerRule)
    if (!('\n' + original.join('\n') + '\n').includes('\n' + expected.join('\n') + '\n')) return []
  }
  return found.some((row) => row.cells.some((cell) => cell.trim())) ? found : []
}

/** A partial chunk can still contain verified row mappings. Recheck their
 * complete quote in the named row range before using those rows as evidence.
 */
export function matchSpreadsheetSourceRows(
  locator: SourceLocator,
  rows: SpreadsheetSourceRow[],
  csv = false,
  sourceMarkdown = '',
): SpreadsheetSourceRow[] {
  const start = locator.row_start,
    end = locator.row_end ?? start
  if (
    locator.type !== 'sheet' ||
    locator.mapping !== 'exact' ||
    !locator.sheet ||
    !start ||
    !end ||
    !Number.isSafeInteger(start) ||
    !Number.isSafeInteger(end) ||
    start < 1 ||
    end < start
  )
    return []
  const quote = sourceQuoteText(locator.quote || '')
  if (!quote) return []
  const candidates = rows
    .filter((row) => row.number >= start && row.number <= end)
    .sort((a, b) => a.number - b.number)
  const rawQuote = (locator.quote || '').trim()
  if (rawQuote.startsWith('|') && rawQuote.endsWith('|')) {
    return matchTableRows(candidates, rawQuote, csv, sourceMarkdown)
  }
  const texts = candidates.map((row) => '| ' + row.cells.join(' | ') + ' |')
  const text = texts.join('\n')
  const match = findInText(text, quote)
  if (!match) return []
  // Normalized text ignores whitespace and table markup. Keep numeric runs
  // separate so changed cells such as [1, 23] cannot match [12, 3].
  const numbers = (value: string) => value.normalize('NFKC').match(/\p{N}+/gu) || []
  if (numbers(quote).join(':') !== numbers(text.slice(match.start, match.end)).join(':')) return []
  let offset = 0
  return candidates.filter((_, index) => {
    const start = offset
    offset += texts[index]!.length + 1
    return start < match.end && start + texts[index]!.length > match.start
  })
}

/** Sheet headings describe workbook structure, not cell rows. They may be
 * excluded from coverage only when they name a sheet whose rows were verified.
 * Every other visible character must remain covered by the verified quotes.
 */
export function spreadsheetScopeCovered(
  request: SourceLocateRequest,
  verified: SourceLocator[],
): boolean {
  const scope = request.sourceMarkdown || request.scope || request.quotes.join('\n')
  if (!scope || !verified.length) return false
  const names = new Set(verified.map((locator) => locator.sheet))
  const body = scope
    .split('\n')
    .filter((line) => {
      const heading = /^\s{0,3}#{1,6}[ \t]+(.+?)[ \t]*$/.exec(line)
      // Closing ATX hashes require separating whitespace; Stock# is a literal
      // sheet name, while "Stock# ###" has a closing heading delimiter.
      return !heading || !names.has(heading[1]!.replace(/[ \t]+#+[ \t]*$/, ''))
    })
    .join('\n')
  const quotes = verified.map((locator) => (locator.quote || '').trim())
  if (quotes.every((quote) => quote.startsWith('|') && quote.endsWith('|'))) {
    return quoteText(body) === quoteText(quotes.join(' '))
  }
  const text = normalizeForMatch(sourceQuoteText(body))
  return (
    !!text &&
    text ===
      normalizeForMatch(verified.map((locator) => sourceQuoteText(locator.quote || '')).join('\n'))
  )
}
