import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import ts from 'typescript'
import * as XLSX from 'xlsx'
import { DOMParser } from '@xmldom/xmldom'
import { matchSpreadsheetSourceRows, spreadsheetScopeCovered } from '../utils/spreadsheetSource'
import type { SourceLocateRequest, SourceLocator } from '../utils/sourceLocator'
import { findInText } from '../utils/sourceLocator'

const source = readFileSync(new URL('./document-preview.vue', import.meta.url), 'utf8')
const start = source.indexOf('function sheetRows(')
const end = source.indexOf('\nfunction locateEpub(', start)
const applyStart = source.indexOf('function applyLocate(')
const applyEnd = source.indexOf('\nwatch(', applyStart)
assert.ok(start >= 0 && end > start && applyStart >= 0 && applyEnd > applyStart)
const compiled = ts.transpileModule(
  `${source.slice(start, end)}\n${source.slice(applyStart, applyEnd)}`,
  {
    compilerOptions: { target: ts.ScriptTarget.ES2022 },
  },
).outputText

type Row = { number: number; text: string }
type LocateResult = { found: boolean; precise: boolean; granularity?: string; reason?: string }

const firstRows = [
  'ROOT SKU',
  ...Array.from({ length: 18 }, (_, i) => `TEST${String(i + 1).padStart(2, '0')}-M01-V00`),
]
const quote = firstRows.map((text) => `| ${text} |`).join('\n')
const locator: SourceLocator = {
  type: 'sheet',
  sheet: 'Sheet1',
  mapping: 'exact',
  partial: true,
  row_start: 1,
  row_end: 19,
  quote,
}
const request: SourceLocateRequest = {
  token: 1,
  locators: [locator],
  quotes: [],
  scope: `# Sheet1\n${quote}`,
}

function fixture(sheets = [{ name: 'Sheet1', texts: firstRows }], sanitizedIds = true) {
  // Exercise the native SheetJS parse/render path with synthetic workbooks.
  // The small DOM adapter supplies selectors/closest that xmldom omits;
  // scrolling and highlight effects are observed separately below.
  const book = XLSX.utils.book_new()
  for (const sheet of sheets) {
    XLSX.utils.book_append_sheet(
      book,
      XLSX.utils.aoa_to_sheet(sheet.texts.map((text) => [text])),
      sheet.name,
    )
  }
  const parsed = XLSX.read(XLSX.write(book, { type: 'buffer', bookType: 'xlsx' }), {
    type: 'buffer',
  })
  const tables = parsed.SheetNames.map((name, index) => {
    const html = XLSX.utils.sheet_to_html(parsed.Sheets[name]!, { id: `sheet-${index}` })
    const doc = new DOMParser().parseFromString(html, 'text/html')
    const cells: { id: string; closest: () => Row }[] = []
    const rows = Array.from(doc.getElementsByTagName('tr')).flatMap((tr) => {
      const tds = Array.from(tr.getElementsByTagName('td'))
      const first = tds.find((td) => /-[A-Z]+(\d+)$/.test(td.getAttribute('id') || ''))
      if (!first) return []
      const row = {
        number: Number(/-[A-Z]+(\d+)$/.exec(first.getAttribute('id')!)![1]),
        text: tds.map((td) => td.textContent || '').join(' '),
      }
      cells.push(
        ...tds.map((td) => ({
          id: `${sanitizedIds ? 'user-content-' : ''}${td.getAttribute('id')}`,
          closest: () => row,
        })),
      )
      return [row]
    })
    return { rows, querySelectorAll: () => cells }
  })
  const box = {
    text: sheets.map((sheet) => `${sheet.name}\n${sheet.texts.join('\n')}`).join('\n'),
    querySelector(selector: string) {
      const match = /^#(user-content-)?sheet-(\d+)$/.exec(selector)
      return match && !!match[1] === sanitizedIds ? tables[Number(match[2])] : null
    },
  }
  const marks: Row[][] = [],
    revealed: Row[] = [],
    fallbacks: string[][] = [],
    events: LocateResult[] = []
  const props = { locate: request }
  let clears = 0
  const ctx = runInNewContext(`${compiled}\n({ locateExcel, applyLocate })`, {
    previewContent: { value: box },
    previewType: { value: 'excel' },
    excelSheetNames: sheets.map((sheet) => sheet.name),
    props,
    matchSpreadsheetSourceRows,
    spreadsheetScopeCovered,
    buildTextIndex: (row: { text: string }) => ({ text: row.text }),
    commitMarks: (_ranges: unknown[], rows: Row[]) => marks.push(rows),
    clearLocateMarks: () => {
      marks.length = 0
      clears++
    },
    reveal: (_box: unknown, row: Row) => revealed.push(row),
    NOT_FOUND: { found: false, precise: false },
    locateQuote: (root: typeof box, _box: unknown, quotes: string[]) => {
      fallbacks.push(quotes)
      const found = quotes.some((quote) => !!findInText(root.text, quote))
      return { found, precise: found }
    },
    evidenceQuotes: (req: SourceLocateRequest) => (req.scope ? [req.scope] : req.quotes),
    emit: (_name: string, value: LocateResult) => events.push(value),
    console,
  }) as { locateExcel(request: SourceLocateRequest): LocateResult; applyLocate(): void }
  return {
    ...ctx,
    props,
    tables,
    marks,
    revealed,
    fallbacks,
    events,
    get clears() {
      return clears
    },
  }
}

test('partial Sheet1 citation reveals and highlights rows 1–19 as a source region', () => {
  for (const sanitized of [true, false]) {
    const f = fixture(undefined, sanitized)
    const result = f.locateExcel(request)
    assert.equal(result.found, true)
    assert.equal(result.precise, false)
    assert.equal(result.granularity, 'block')
    assert.equal(result.reason, undefined)
    assert.deepEqual(Array.from(f.marks[0]), f.tables[0].rows)
    assert.equal(f.revealed[0], f.tables[0].rows[0])
    assert.equal(f.fallbacks.length, 0)
  }
})

test('a real unmapped paragraph retains the partial-coverage warning', () => {
  const f = fixture()
  const result = f.locateExcel({
    ...request,
    scope: `${request.scope}\nUnmapped business paragraph`,
  })
  assert.equal(result.found, true)
  assert.equal(result.reason, 'partial')
  assert.equal(f.marks[0]?.length, 19)
})

test('identical text in other sheets cannot replace the named sheet', () => {
  const f = fixture([
    { name: 'OtherSheet', texts: firstRows },
    { name: 'Sheet1', texts: firstRows },
  ])
  f.locateExcel(request)
  assert.deepEqual(Array.from(f.marks[0]), f.tables[1].rows)
  assert.equal(f.revealed[0], f.tables[1].rows[0])
})

test('changed rows or a missing sheet remain unresolved without highlighting stale coordinates', () => {
  for (const sheets of [
    [{ name: 'Sheet1', texts: firstRows.map((text, i) => (i === 5 ? 'Changed SKU' : text)) }],
    [{ name: 'OtherSheet', texts: firstRows }],
  ]) {
    const f = fixture(sheets)
    assert.equal(f.locateExcel(request).found, false)
    assert.equal(f.marks.length, 0)
    assert.equal(f.fallbacks.length, 0)
  }
})

test('one missing locator cannot make another verified locator claim full coverage', () => {
  const f = fixture()
  const result = f.locateExcel({
    ...request,
    locators: [locator, { ...locator, sheet: 'MissingSheet' }],
  })
  assert.equal(result.found, true)
  assert.equal(result.reason, 'partial')
})

test('a missing named sheet cannot resolve to coincidental text in another sheet', () => {
  const f = fixture([{ name: 'OtherSheet', texts: firstRows }])
  const result = f.locateExcel({ ...request, scope: quote })
  assert.equal(result.found, false)
  assert.equal(f.marks.length, 0)
  assert.equal(f.fallbacks.length, 0)
})

test('legacy citations without structural mappings retain exact text fallback', () => {
  const f = fixture()
  const result = f.locateExcel({ ...request, locators: [], scope: quote })
  assert.equal(result.found, true)
  assert.equal(result.precise, true)
  assert.equal(f.fallbacks.length, 1)
})

test('the existing highlight limit reports truncation and avoids an unbounded row-number loop', () => {
  const texts = Array.from({ length: 205 }, (_, i) => `SKU${i}-M01-V00`)
  const longQuote = texts.join('\n')
  const loc = { ...locator, row_end: Number.MAX_SAFE_INTEGER, quote: longQuote }
  const f = fixture([{ name: 'Sheet1', texts }])
  const result = f.locateExcel({ ...request, scope: longQuote, locators: [loc] })
  assert.equal(result.found, true)
  assert.equal(result.reason, 'partial')
  assert.equal(f.marks[0]?.length, 201)
})

test('a quoted row late in a broad mapping is not hidden by the highlight limit', () => {
  const texts = Array.from({ length: 450 }, (_, i) => `SKU${i}-M01-V00`)
  const loc = { ...locator, row_end: 450, quote: texts[350] }
  const f = fixture([{ name: 'Sheet1', texts }])
  const result = f.locateExcel({ ...request, scope: loc.quote, locators: [loc] })
  assert.equal(result.found, true)
  assert.equal(result.reason, undefined)
  assert.deepEqual(Array.from(f.marks[0]), [f.tables[0].rows[350]])
})

test('reselecting a citation clears old marks and unavailable source revisions remain blocked', () => {
  const f = fixture()
  f.applyLocate()
  assert.equal(f.marks[0]?.length, 19)
  f.props.locate = { ...request, token: 2, unavailable: true }
  f.applyLocate()
  assert.equal(f.clears, 2)
  assert.equal(f.marks.length, 0)
  const event = f.events.at(-1)
  assert.ok(event)
  assert.equal(event.found, false)
  assert.equal(event.reason, 'unavailable')
})

test('non-partial mappings also reject changed source content', () => {
  const f = fixture([
    { name: 'Sheet1', texts: firstRows.map((text, i) => (i === 5 ? 'Changed SKU' : text)) },
  ])
  assert.equal(
    f.locateExcel({ ...request, locators: [{ ...locator, partial: false }] }).found,
    false,
  )
  assert.equal(f.marks.length, 0)
  assert.equal(f.fallbacks.length, 0)
})

test('verified mappings on multiple sheets highlight only their own rows', () => {
  const f = fixture([
    { name: 'Sheet1', texts: firstRows },
    { name: 'Sheet2', texts: ['Another SKU'] },
  ])
  const second = { ...locator, sheet: 'Sheet2', row_start: 1, row_end: 1, quote: '| Another SKU |' }
  const result = f.locateExcel({
    ...request,
    locators: [locator, second],
    scope: `${request.scope}\n# Sheet2\n${second.quote}`,
  })
  assert.equal(result.reason, undefined)
  assert.deepEqual(Array.from(f.marks[0]), [...f.tables[0]!.rows, ...f.tables[1]!.rows])
})
