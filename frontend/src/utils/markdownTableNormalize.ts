/** Matches a GFM alignment cell (---, :---, ---:, :---:). */
const SEPARATOR_CELL = /^:?-{3,}:?$/;

function splitRowCells(line: string): string[] {
  const inner = line.trim();
  if (!inner.startsWith('|')) {
    return [];
  }
  let parts = inner.split('|');
  if (parts.length && parts[0].trim() === '') {
    parts = parts.slice(1);
  }
  if (parts.length && parts[parts.length - 1].trim() === '') {
    parts = parts.slice(0, -1);
  }
  return parts.map((part) => part.trim());
}

function isTableRow(line: string): boolean {
  // Four-space and tab-indented rows are literal code, not Markdown tables.
  if (!/^ {0,3}\|/.test(line)) {
    return false;
  }
  const stripped = line.trim();
  return stripped.startsWith('|') && stripped.includes('|', 1);
}

function isSeparatorRow(line: string): boolean {
  const cells = splitRowCells(line);
  return cells.length > 0 && cells.every((cell) => SEPARATOR_CELL.test(cell));
}

function isEmptyRow(line: string): boolean {
  const cells = splitRowCells(line);
  return cells.length > 0 && cells.every((cell) => cell === '');
}

function separatorRowFor(headerLine: string): string {
  const cells = splitRowCells(headerLine);
  return `| ${cells.map(() => '---').join(' | ')} |`;
}

function normalizeTableBlock(block: string[]): string[] {
  let rows = [...block];
  while (rows.length && isEmptyRow(rows[0])) {
    rows.shift();
  }
  if (rows.length && isSeparatorRow(rows[0])) {
    rows.shift();
  }
  if (rows.length >= 2 && !isSeparatorRow(rows[1])) {
    rows = [rows[0], separatorRowFor(rows[0]), ...rows.slice(1)];
  }
  return rows;
}

/** Fix MarkItDown-style tables outside literal fenced and indented code. */
export function normalizeSpuriousTablePrefixes(content: string): string {
  const lines = content.split('\n');
  const out: string[] = [];
  let i = 0;
  let fence: string | undefined;
  while (i < lines.length) {
    const marker = /^ {0,3}(`{3,}|~{3,})(.*)$/.exec(lines[i]);
    if (fence) {
      if (marker && marker[1][0] === fence[0] && marker[1].length >= fence.length && /^[ \t\r]*$/.test(marker[2])) {
        fence = undefined;
      }
      out.push(lines[i]);
      i += 1;
      continue;
    }
    if (marker && (marker[1][0] === '~' || !marker[2].includes('`'))) {
      fence = marker[1];
      out.push(lines[i]);
      i += 1;
      continue;
    }
    if (!isTableRow(lines[i])) {
      out.push(lines[i]);
      i += 1;
      continue;
    }
    const block: string[] = [];
    while (i < lines.length && isTableRow(lines[i])) {
      block.push(lines[i]);
      i += 1;
    }
    out.push(...normalizeTableBlock(block));
  }
  return out.join('\n');
}
