import { writeFileSync } from 'node:fs'

export function escapeLocaleString(value: string): string {
  return `'${value
    .replace(/\\/g, '\\\\')
    .replace(/'/g, "\\'")
    .replace(/\r/g, '\\r')
    .replace(/\n/g, '\\n')
    .replace(/\u2028/g, '\\u2028')
    .replace(/\u2029/g, '\\u2029')}'`
}

export function quoteLocaleKey(key: string): string {
  if (/^[A-Za-z_][\w]*$/.test(key)) return key
  return escapeLocaleString(key)
}

export function serializeLocaleValue(value: unknown, indent = 0): string {
  const pad = '  '.repeat(indent)

  if (typeof value === 'string') return escapeLocaleString(value)

  if (Array.isArray(value)) {
    const items = value.map((item) => `${pad}  ${serializeLocaleValue(item, indent + 1)}`)
    return `[\n${items.join(',\n')}\n${pad}]`
  }

  if (!value || typeof value !== 'object') return 'null'

  const entries = Object.entries(value as Record<string, unknown>).map(([key, child]) => {
    const serialized = serializeLocaleValue(child, indent + 1)
    return `${pad}  ${quoteLocaleKey(key)}: ${serialized}`
  })

  return `{\n${entries.join(',\n')}\n${pad}}`
}

export function writeLocaleModule(file: string, tree: unknown): void {
  writeFileSync(file, `export default ${serializeLocaleValue(tree, 0)}\n`, 'utf8')
}
