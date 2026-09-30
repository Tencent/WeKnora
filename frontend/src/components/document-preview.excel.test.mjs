import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import * as XLSX from 'xlsx'

import {
  EXCEL_PREVIEW_PARSE_ROWS,
  isExcelPreviewTooLarge,
} from '../utils/filePreview.ts'

const source = readFileSync(new URL('./document-preview.vue', import.meta.url), 'utf8')

test('large worksheets are capped before the preview renders HTML', () => {
  const start = source.indexOf('async function renderExcel(')
  const end = source.indexOf('\nasync function renderText(', start)
  assert.ok(start >= 0 && end > start)
  const renderExcel = source.slice(start, end)
  assert.match(renderExcel, /sheetRows: EXCEL_PREVIEW_PARSE_ROWS/)

  const workbook = XLSX.utils.book_new()
  const worksheet = XLSX.utils.aoa_to_sheet([
    ['row', 'value'],
    ...Array.from({ length: EXCEL_PREVIEW_PARSE_ROWS + 100 }, (_, index) => [index + 1, `value-${index}`]),
  ])
  XLSX.utils.book_append_sheet(workbook, worksheet, 'data')
  const bytes = XLSX.write(workbook, { type: 'buffer', bookType: 'xlsx', compression: true })
  const parsed = XLSX.read(bytes, { type: 'buffer', sheetRows: EXCEL_PREVIEW_PARSE_ROWS })
  const range = XLSX.utils.decode_range(parsed.Sheets.data['!ref'])
  const metrics = [{
    rows: range.e.r - range.s.r + 1,
    columns: range.e.c - range.s.c + 1,
  }]

  assert.equal(metrics[0].rows, EXCEL_PREVIEW_PARSE_ROWS)
  assert.equal(isExcelPreviewTooLarge(bytes.length, metrics), true)
})
