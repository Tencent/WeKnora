export interface HeaderRow {
  key: string
  value: string
}

/** Parses "Name: Value" lines; lines without a colon keep an empty value. */
export function parseHeaders(text: unknown): HeaderRow[] {
  if (typeof text !== 'string' || !text.trim()) return []
  return text
    .split('\n')
    .filter(line => line.trim())
    .map(line => {
      const i = line.indexOf(':')
      if (i < 0) return { key: line.trim(), value: '' }
      return { key: line.slice(0, i).trim(), value: line.slice(i + 1).trim() }
    })
}

/** Serializes rows back to "Name: Value" lines, skipping rows without a name. */
export function serializeHeaders(rows: HeaderRow[]): string {
  return rows
    .filter(r => r.key.trim())
    .map(r => `${r.key.trim()}: ${r.value}`)
    .join('\n')
}
