import type { ComposerTranslation } from 'vue-i18n'

export type CatalogDisplayType =
  | 'find_operations'
  | 'run_operation'
  | 'search_weknora_docs'
  | 'read_weknora_doc'

const PAYLOAD_KEYS = [
  'operations', 'unavailable',
  'operation', 'result',
  'pages', 'results',
  'path', 'title', 'category', 'anchor', 'section', 'url', 'content', 'outline',
  'truncated', 'offset', 'error',
] as const

export type OperationParamRow = {
  name: string
  type: string
  required: boolean
  description: string
  enumText: string
  range: { min: number, max: number } | null
}

export type OperationRow = {
  id: string
  risk: string
  summary: string
  description: string
  reason: string
  expandable: boolean
  params: OperationParamRow[]
}

export type DocPageRow = {
  path: string
  title: string
  category: string
  url: string
}

export type DocHitRow = DocPageRow & {
  anchor: string
  section: string
  snippet: string
}

export type DocOutlineRow = {
  anchor: string
  heading: string
  level: number
}

export type DocReadView = {
  title: string
  path: string
  section: string
  url: string
  content: string
  outline: DocOutlineRow[]
  truncated: boolean
  error: string
}

export type RunOperationView = {
  operation: string
  data: unknown
  meta: Record<string, unknown>
  truncated: boolean
}

type ToolEventLike = {
  tool_name?: string
  arguments?: unknown
  output?: string
  tool_data?: unknown
  pending?: boolean
  success?: boolean
}

function record(value: unknown): Record<string, unknown> {
  if (typeof value === 'string') {
    const parsed = parseJsonObject(value)
    return parsed || {}
  }
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : {}
}

function parseJsonObject(value: unknown): Record<string, unknown> | null {
  if (typeof value !== 'string') return null
  const text = value.trim()
  if (!text.startsWith('{')) return null
  try {
    const parsed = JSON.parse(text)
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return parsed as Record<string, unknown>
    }
  } catch {
    /* Plain error text, or a truncated historical output. */
  }
  return null
}

function stringField(source: Record<string, unknown>, key: string): string {
  const value = source[key]
  return typeof value === 'string' ? value.trim() : ''
}

function numberOrNull(value: unknown): number | null {
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

export function getCatalogToolDisplayType(toolName?: string): CatalogDisplayType | undefined {
  if (toolName === 'find_operations' || toolName === 'run_operation' ||
    toolName === 'search_weknora_docs' || toolName === 'read_weknora_doc') {
    return toolName
  }
  return undefined
}

/** Reads the JSON these tools put in output. Live SSE also copies the event envelope onto tool_data. */
export function parseCatalogPayload(output?: string, data?: unknown): Record<string, unknown> {
  const extra = record(data)
  const fromOutput = parseJsonObject(output) || {}
  const fromEnvelope = parseJsonObject(extra.output) || {}
  const overlay: Record<string, unknown> = {}
  for (const key of PAYLOAD_KEYS) {
    if (key in extra) overlay[key] = extra[key]
  }
  return { ...fromEnvelope, ...fromOutput, ...overlay }
}

function paramRows(value: unknown): OperationParamRow[] {
  if (!Array.isArray(value)) return []
  return value.flatMap((item) => {
    const row = record(item)
    const name = stringField(row, 'name')
    if (!name) return []
    const enumText = Array.isArray(row.enum)
      ? row.enum.map((entry) => String(entry ?? '').trim()).filter(Boolean).join(', ')
      : ''
    const min = numberOrNull(row.minimum)
    const max = numberOrNull(row.maximum)
    return [{
      name,
      type: stringField(row, 'type'),
      required: row.required === true,
      description: stringField(row, 'description'),
      enumText,
      range: min != null && max != null ? { min, max } : null,
    }]
  })
}

function operationRow(value: unknown, withReason = false): OperationRow | null {
  const row = record(value)
  const id = stringField(row, 'id')
  if (!id) return null
  const params = paramRows(row.params)
  const description = stringField(row, 'description')
  const reason = withReason ? stringField(row, 'reason') : ''
  return {
    id,
    risk: stringField(row, 'risk'),
    summary: stringField(row, 'summary'),
    description,
    reason,
    expandable: Boolean(description || params.length),
    params,
  }
}

export function operationRows(data: Record<string, unknown>): OperationRow[] {
  const source = data.operations
  if (!Array.isArray(source)) return []
  return source.flatMap((item) => {
    const row = operationRow(item)
    return row ? [row] : []
  })
}

export function unavailableOperationRows(data: Record<string, unknown>): OperationRow[] {
  const source = data.unavailable
  if (!Array.isArray(source)) return []
  return source.flatMap((item) => {
    const row = operationRow(item, true)
    return row ? [row] : []
  })
}

export function runOperationView(data: Record<string, unknown>): RunOperationView {
  const result = record(data.result)
  const hasEnvelope = 'data' in result || 'meta' in result || 'truncated' in result
  return {
    operation: stringField(data, 'operation'),
    data: hasEnvelope ? result.data : (data.result === undefined ? undefined : data.result),
    meta: record(result.meta),
    truncated: result.truncated === true,
  }
}

export function isEmptyCatalogValue(value: unknown): boolean {
  if (value == null) return true
  if (typeof value === 'string') return value.trim() === ''
  if (Array.isArray(value)) return value.length === 0
  if (typeof value === 'object') return Object.keys(value).length === 0
  return false
}

function docPage(value: unknown): DocPageRow | null {
  const row = record(value)
  const path = stringField(row, 'path')
  const title = stringField(row, 'title') || path
  if (!title && !path) return null
  return {
    path,
    title,
    category: stringField(row, 'category'),
    url: stringField(row, 'url'),
  }
}

export function docPages(data: Record<string, unknown>): DocPageRow[] {
  if (!Array.isArray(data.pages)) return []
  return data.pages.flatMap((item) => {
    const row = docPage(item)
    return row ? [row] : []
  })
}

export function docHits(data: Record<string, unknown>): DocHitRow[] {
  if (!Array.isArray(data.results)) return []
  return data.results.flatMap((item) => {
    const row = record(item)
    const page = docPage({ ...row, title: stringField(row, 'section') || stringField(row, 'path') })
    if (!page) return []
    return [{
      ...page,
      title: stringField(row, 'section') || page.path,
      anchor: stringField(row, 'anchor'),
      section: stringField(row, 'section'),
      snippet: stringField(row, 'snippet'),
    }]
  })
}

export function docReadView(data: Record<string, unknown>): DocReadView {
  const outline = Array.isArray(data.outline) ? data.outline.flatMap((item) => {
    const row = record(item)
    const heading = stringField(row, 'heading')
    if (!heading) return []
    const level = numberOrNull(row.level) ?? 2
    return [{ anchor: stringField(row, 'anchor'), heading, level }]
  }) : []
  return {
    title: stringField(data, 'title'),
    path: stringField(data, 'path'),
    section: stringField(data, 'section'),
    url: stringField(data, 'url'),
    content: stringField(data, 'content'),
    outline,
    truncated: data.truncated === true,
    error: stringField(data, 'error'),
  }
}

export function safeDocUrl(value: string): string {
  const raw = value.trim()
  if (!/^https?:\/\//i.test(raw)) return ''
  try {
    const url = new URL(raw)
    return url.protocol === 'http:' || url.protocol === 'https:' ? url.href : ''
  } catch {
    return ''
  }
}

export function getCatalogToolTitle(t: ComposerTranslation, event: ToolEventLike): string {
  const type = getCatalogToolDisplayType(event.tool_name)
  if (!type) return ''
  const args = record(event.arguments)
  const data = parseCatalogPayload(event.output, event.tool_data)
  let title = ''
  if (type === 'find_operations') {
    const base = t('agentStream.tools.findOperations')
    const detail = [stringField(args, 'query'), stringField(args, 'domain')].filter(Boolean).join(' · ')
    title = detail ? `${base}：「${detail}」` : base
  } else if (type === 'run_operation') {
    const base = t('agentStream.tools.runOperation')
    const operation = stringField(args, 'operation') || stringField(data, 'operation')
    title = operation ? `${base}：${operation}` : base
  } else if (type === 'search_weknora_docs') {
    const base = t('agentStream.tools.searchWeKnoraDocs')
    const detail = stringField(args, 'query') || stringField(args, 'category')
    title = detail ? `${base}：「${detail}」` : base
  } else {
    const base = t('agentStream.tools.readWeKnoraDoc')
    const detail = stringField(data, 'section') || stringField(data, 'title') || stringField(args, 'path')
    title = detail ? `${base}：「${detail}」` : base
  }
  if (event.pending) return t('agentStream.toolStatus.calling', { name: title })
  if (event.success === false) return t('agentStream.toolStatus.calledFailed', { name: title })
  return title
}
