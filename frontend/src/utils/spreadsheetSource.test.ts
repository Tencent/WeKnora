import assert from 'node:assert/strict'
import test from 'node:test'
import { matchSpreadsheetSourceRows, spreadsheetScopeCovered } from './spreadsheetSource'
import type { SourceLocateRequest, SourceLocator } from './sourceLocator'

const rows = [
  { number: 1, text: 'ROOT SKU' },
  ...Array.from({ length: 18 }, (_, i) => ({
    number: i + 2,
    text: `TEST${String(i + 1).padStart(2, '0')}-M01-V00`,
  })),
]
const quote = rows.map((row) => `| ${row.text} |`).join('\n')
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
  scope: `# Sheet1\n\n${quote}`,
}

test('a sheet heading does not discard verified rows 1–19 of a partial citation', () => {
  assert.deepEqual(matchSpreadsheetSourceRows(locator, rows), rows)
  assert.equal(spreadsheetScopeCovered(request, [locator]), true)
  assert.equal(locator.partial, true, 'keep the backend mapping contract intact')
})

test('complete mappings also require matching source text', () => {
  assert.deepEqual(matchSpreadsheetSourceRows({ ...locator, partial: false }, rows), rows)
  assert.deepEqual(
    matchSpreadsheetSourceRows(
      locator,
      rows.map((row) => (row.number === 5 ? { ...row, text: 'Changed SKU' } : row)),
    ),
    [],
  )
  assert.deepEqual(matchSpreadsheetSourceRows({ ...locator, quote: undefined }, rows), [])
})

test('identical text outside the cited row range is not evidence', () => {
  assert.deepEqual(
    matchSpreadsheetSourceRows(
      locator,
      rows.map((row) => ({ ...row, number: row.number + 20 })),
    ),
    [],
  )
  assert.deepEqual(
    matchSpreadsheetSourceRows(
      locator,
      rows.filter((row) => row.number !== 5),
    ),
    [],
  )
})

test('a broad row range highlights only rows intersecting the verified quote', () => {
  const broad = [
    { number: 1, text: 'Unrelated before' },
    { number: 2, text: 'Verified SKU' },
    { number: 3, text: 'Unrelated after' },
  ]
  assert.deepEqual(
    matchSpreadsheetSourceRows({ ...locator, row_end: 3, quote: 'Verified SKU' }, broad),
    [broad[1]],
  )
})

test('short repeated quotes and changed numeric values remain unresolved', () => {
  const loc = { ...locator, row_start: 1, row_end: 2, quote: 'SKU 1.5' }
  assert.deepEqual(matchSpreadsheetSourceRows(loc, [{ number: 1, text: 'SKU 15' }]), [])
  assert.deepEqual(
    matchSpreadsheetSourceRows(loc, [
      { number: 1, text: 'SKU 1.5' },
      { number: 2, text: 'SKU 1.5' },
    ]),
    [],
  )
  assert.deepEqual(
    matchSpreadsheetSourceRows({ ...loc, quote: '| 1 | | 23 |' }, [
      { number: 1, text: '12' },
      { number: 2, text: '3' },
    ]),
    [],
  )
})

test('invalid or unverified coordinates are not used', () => {
  for (const change of [
    { row_start: 0 },
    { row_start: 1.5 },
    { row_start: NaN },
    { row_end: 0 },
    { row_end: Infinity },
    { mapping: '' },
    { type: 'slide' },
    { sheet: '' },
  ]) {
    assert.deepEqual(matchSpreadsheetSourceRows({ ...locator, ...change }, rows), [])
  }
})

test('only verified sheet headings may be excluded from coverage', () => {
  for (const heading of ['# OtherSheet', '# Additional instructions', 'Sheet1']) {
    assert.equal(
      spreadsheetScopeCovered({ ...request, scope: `${heading}\n${quote}` }, [locator]),
      false,
    )
  }
  assert.equal(
    spreadsheetScopeCovered(
      { ...request, scope: `# Sheet1\n${quote}\nUnmapped business content` },
      [locator],
    ),
    false,
  )
  assert.equal(spreadsheetScopeCovered(request, []), false)
})

test('literal trailing hashes in sheet names are not mistaken for heading delimiters', () => {
  const named = { ...locator, sheet: 'Stock#' }
  assert.equal(spreadsheetScopeCovered({ ...request, scope: `# Stock#\n${quote}` }, [named]), true)
  assert.equal(
    spreadsheetScopeCovered({ ...request, scope: `# Sheet1 ###\n${quote}` }, [locator]),
    true,
  )
})

test('multiple sheets require every sheet’s own verified content', () => {
  const second = {
    ...locator,
    sheet: 'Sheet2',
    row_start: 1,
    row_end: 1,
    quote: '| Another SKU |',
  }
  const multi = { ...request, scope: `${request.scope}\n# Sheet2\n${second.quote}` }
  assert.equal(spreadsheetScopeCovered(multi, [locator, second]), true)
  assert.equal(spreadsheetScopeCovered(multi, [locator]), false)
})

test('original Markdown bounds coverage when an excerpt alone would hide missing content', () => {
  assert.equal(
    spreadsheetScopeCovered(
      { ...request, sourceMarkdown: `${request.scope}\nUnmapped content`, scope: quote },
      [locator],
    ),
    false,
  )
})
