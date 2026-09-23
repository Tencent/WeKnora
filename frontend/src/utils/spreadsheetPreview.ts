import * as XLSX from 'xlsx';

import { PREVIEW_ROWS, PREVIEW_COLUMNS, PREVIEW_CELL_CHARACTERS, type SpreadsheetSheet, type SpreadsheetPage } from './spreadsheetPreviewLimits';

export function readSpreadsheet(buffer: ArrayBuffer, type = ''): XLSX.WorkBook {
  const options: XLSX.ParsingOptions = { cellHTML: false, cellFormula: false, cellStyles: false };
  type = type.toLowerCase();
  if (['csv', 'tsv', 'tab'].includes(type)) {
    let text: string;
    try { text = new TextDecoder('utf-8', { fatal: true }).decode(buffer); }
    catch { text = new TextDecoder('gb18030').decode(buffer); }
    return XLSX.read(text, { ...options, type: 'string', ...(['tsv', 'tab'].includes(type) ? { FS: '\t' } : {}) });
  }
  return XLSX.read(buffer, { ...options, type: 'array' });
}

// Formatting-only cells can inflate !ref to an entire Excel worksheet. Bound
// navigation to actual values, without iterating millions of empty addresses.
export function spreadsheetRanges(book: XLSX.WorkBook): SpreadsheetSheet[] {
  return book.SheetNames.map(name => {
    let rows = 0, columns = 0;
    const sheet = book.Sheets[name];
    for (const address of Object.keys(sheet)) {
      if (address.startsWith('!')) continue;
      const cell = sheet[address];
      if ((cell?.v == null || cell.v === '') && !cell?.w) continue;
      const pos = XLSX.utils.decode_cell(address);
      rows = Math.max(rows, pos.r + 1);
      columns = Math.max(columns, pos.c + 1);
    }
    return { name, rows, columns };
  });
}

export function spreadsheetPage(book: XLSX.WorkBook, range: SpreadsheetSheet, rowPage = 1, columnPage = 1): SpreadsheetPage {
  const page = Math.max(1, Math.min(Math.ceil(range.rows / PREVIEW_ROWS) || 1, Math.floor(rowPage) || 1));
  const columnsPage = Math.max(1, Math.min(Math.ceil(range.columns / PREVIEW_COLUMNS) || 1, Math.floor(columnPage) || 1));
  const rowStart = (page - 1) * PREVIEW_ROWS;
  const columnStart = (columnsPage - 1) * PREVIEW_COLUMNS;
  const sheet = book.Sheets[range.name];
  const columns = Array.from({ length: Math.min(PREVIEW_COLUMNS, range.columns - columnStart) }, (_, i) => XLSX.utils.encode_col(columnStart + i));
  let truncated = false;
  const rows = Array.from({ length: Math.min(PREVIEW_ROWS, range.rows - rowStart) }, (_, i) => ({
    number: rowStart + i + 1,
    cells: columns.map((_, j) => {
      const cell = sheet[XLSX.utils.encode_cell({ r: rowStart + i, c: columnStart + j })];
      const value = cell ? String(cell.w ?? XLSX.utils.format_cell(cell)) : '';
      if (value.length > PREVIEW_CELL_CHARACTERS) {
        truncated = true;
        return value.slice(0, PREVIEW_CELL_CHARACTERS) + '…';
      }
      return value;
    }),
  }));
  return { columns, rows, page, columnPage: columnsPage, truncated };
}
