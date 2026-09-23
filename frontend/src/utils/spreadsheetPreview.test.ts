import assert from 'node:assert/strict';
import test from 'node:test';
import * as XLSX from 'xlsx';
import { readSpreadsheet, spreadsheetRanges, spreadsheetPage } from './spreadsheetPreview';

test('ignores full-sheet formatting ranges and retains zero and false values', () => {
  const book = { SheetNames: ['Sheet'], Sheets: { Sheet: {
    '!ref': 'A1:XFD1048576', A1: { t: 's', v: '中文' }, B3: { t: 'n', v: 0 },
    C4: { t: 'b', v: false }, XFD1048576: { t: 's', v: '' },
  } } } as XLSX.WorkBook;
  const [range] = spreadsheetRanges(book);
  assert.deepEqual(range, { name: 'Sheet', rows: 4, columns: 3 });
  const page = spreadsheetPage(book, range);
  assert.equal(page.rows[2].cells[1], '0');
  assert.equal(page.rows[3].cells[2], 'FALSE');
});

test('pages rows and columns with a hard bound including sparse edge cells', () => {
  const sheet = XLSX.utils.aoa_to_sheet([['start']]);
  sheet['AZ123'] = { t: 's', v: 'last' };
  const book = { SheetNames: ['one'], Sheets: { one: sheet } };
  const [range] = spreadsheetRanges(book);
  const first = spreadsheetPage(book, range);
  assert.equal(first.rows.length, 50);
  assert.equal(first.columns.length, 25);
  const last = spreadsheetPage(book, range, 999, 999);
  assert.equal(last.page, 3);
  assert.equal(last.columnPage, 3);
  assert.equal(last.rows.at(-1)?.number, 123);
  assert.equal(last.rows.at(-1)?.cells.at(-1), 'last');
});

test('reads binary spreadsheets and delimited Chinese text without HTML generation', () => {
  const book = XLSX.utils.book_new();
  XLSX.utils.book_append_sheet(book, XLSX.utils.aoa_to_sheet([['中文', '<img src=x onerror=alert(1)>']]), '测试');
  const parsed = readSpreadsheet(XLSX.write(book, { bookType: 'xlsx', type: 'array' }), 'xlsx');
  assert.equal(spreadsheetPage(parsed, spreadsheetRanges(parsed)[0]).rows[0].cells[1], '<img src=x onerror=alert(1)>');
  const tsv = readSpreadsheet(new TextEncoder().encode('名称\t状态\n中文\t正常').buffer, 'tsv');
  assert.equal(spreadsheetPage(tsv, spreadsheetRanges(tsv)[0]).rows[1].cells[1], '正常');
});

test('empty sheets and very large cell text stay bounded', () => {
  const book = { SheetNames: ['empty', 'long'], Sheets: { empty: {}, long: { A1: { t: 's', v: '中'.repeat(100_000) } } } } as XLSX.WorkBook;
  const ranges = spreadsheetRanges(book);
  assert.equal(spreadsheetPage(book, ranges[0]).rows.length, 0);
  assert.equal(spreadsheetPage(book, ranges[1]).rows[0].cells[0].length, 2001);
});

test('parses a real XLSX with an inflated dimension without enumerating the range', async () => {
  const { default: JSZip } = await import('jszip');
  const book = XLSX.utils.book_new();
  XLSX.utils.book_append_sheet(book, XLSX.utils.aoa_to_sheet([['value']]), 'Sheet');
  const zip = await JSZip.loadAsync(XLSX.write(book, { bookType: 'xlsx', type: 'array' }));
  const path = 'xl/worksheets/sheet1.xml';
  const xml = await zip.file(path)!.async('string');
  zip.file(path, xml.replace(/<dimension ref="[^"]+"\s*\/>/, '<dimension ref="A1:XFD1048576"/>'));
  const parsed = readSpreadsheet(await zip.generateAsync({ type: 'arraybuffer' }), 'xlsx');
  assert.equal(parsed.Sheets.Sheet?.['!ref'], 'A1:XFD1048576');
  const [range] = spreadsheetRanges(parsed);
  assert.deepEqual(range, { name: 'Sheet', rows: 1, columns: 1 });
  assert.equal(spreadsheetPage(parsed, range!).rows.length, 1);
});
