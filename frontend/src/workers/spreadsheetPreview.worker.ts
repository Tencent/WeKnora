import { readSpreadsheet, spreadsheetRanges, spreadsheetPage } from '../utils/spreadsheetPreview';

let workbook: ReturnType<typeof readSpreadsheet>;
let sheets: ReturnType<typeof spreadsheetRanges>;
self.onmessage = ({ data }) => {
  try {
    if (data.type === 'load') {
      workbook = readSpreadsheet(data.buffer, data.fileType);
      sheets = spreadsheetRanges(workbook);
    }
    const sheetIndex = Math.max(0, Math.min(sheets.length - 1, Number(data.sheetIndex) || 0));
    const range = sheets[sheetIndex];
    self.postMessage({ id: data.id, sheets, sheetIndex, page: range ? spreadsheetPage(workbook, range, data.page, data.columnPage) : null });
  } catch (error) {
    self.postMessage({ id: data.id, error: error instanceof Error ? error.message : String(error) });
  }
};
