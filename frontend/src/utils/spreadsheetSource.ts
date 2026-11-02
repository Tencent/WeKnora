import {
  findInText,
  normalizeForMatch,
  sourceQuoteText,
  type SourceLocateRequest,
  type SourceLocator,
} from './sourceLocator'

export type SpreadsheetSourceRow = { number: number; text: string }

/** A partial chunk can still contain verified row mappings. Recheck their
 * complete quote in the named row range before using those rows as evidence.
 */
export function matchSpreadsheetSourceRows(
  locator: SourceLocator,
  rows: SpreadsheetSourceRow[],
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
  const text = candidates.map((row) => row.text).join('\n')
  const match = findInText(text, quote)
  if (!match) return []
  // Normalized text ignores whitespace and table markup. Keep numeric runs
  // separate so changed cells such as [1, 23] cannot match [12, 3].
  const numbers = (value: string) => value.normalize('NFKC').match(/\p{N}+/gu) || []
  if (numbers(quote).join(':') !== numbers(text.slice(match.start, match.end)).join(':')) return []
  let offset = 0
  return candidates.filter((row) => {
    const start = offset
    offset += row.text.length + 1
    return start < match.end && start + row.text.length > match.start
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
  const body = sourceQuoteText(scope)
    .split('\n')
    .filter((line) => {
      const heading = /^\s{0,3}#{1,6}[ \t]+(.+?)[ \t]*$/.exec(line)
      // Closing ATX hashes require separating whitespace; Stock# is a literal
      // sheet name, while "Stock# ###" has a closing heading delimiter.
      return !heading || !names.has(heading[1]!.replace(/[ \t]+#+[ \t]*$/, ''))
    })
    .join('\n')
  const text = normalizeForMatch(body)
  return (
    !!text &&
    text ===
      normalizeForMatch(verified.map((locator) => sourceQuoteText(locator.quote || '')).join('\n'))
  )
}
