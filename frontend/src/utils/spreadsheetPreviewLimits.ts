export const PREVIEW_ROWS = 50;
export const PREVIEW_COLUMNS = 25;
export const PREVIEW_CELL_CHARACTERS = 2000;
export const PREVIEW_MAX_BYTES = 32 * 1024 * 1024;
export const PREVIEW_TIMEOUT_MS = 30_000;

export interface SpreadsheetSheet {
  name: string;
  rows: number;
  columns: number;
}

export interface SpreadsheetPage {
  columns: string[];
  rows: Array<{ number: number; cells: string[] }>;
  page: number;
  columnPage: number;
  truncated: boolean;
}
