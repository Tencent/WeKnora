// Plugin tool result views (toolViews in plugin.yaml): a plugin maps its
// tool's structured result onto one of these generic views, so the chat
// can show it without running plugin code. This module is the pure part of
// PluginToolView.vue.
import { localizedText, type LocalizedText } from '../utils/localizedText'

export type ToolViewKind = 'table' | 'cards' | 'kv' | 'markdown' | 'json' | 'page'

export interface ToolViewColumn {
  field: string
  title?: LocalizedText
  link?: string
}

/** Same shape as manifest.ToolView on the backend. */
export interface ToolViewSpec {
  view: ToolViewKind
  items?: string
  columns?: ToolViewColumn[]
  title?: string
  subtitle?: string
  body?: string
  link?: string
  fields?: ToolViewColumn[]
  field?: string
  entry?: string
}

export interface Cell {
  text: string
  href?: string
}

/** Field at a dotted path ("fields.status"); undefined when absent. */
export function valueAt(value: unknown, path: string | undefined): unknown {
  if (!path) return value
  let v: unknown = value
  for (const key of path.split('.')) {
    if (typeof v !== 'object' || v === null || Array.isArray(v)) return undefined
    v = (v as Record<string, unknown>)[key]
  }
  return v
}

/** A value as a cell's text: scalars as they are, lists joined, objects as JSON. */
export function display(value: unknown): string {
  if (value === undefined || value === null) return ''
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  if (Array.isArray(value) && value.every((v) => typeof v !== 'object' || v === null)) {
    return value.map(display).join(', ')
  }
  try {
    return JSON.stringify(value)
  } catch {
    return String(value)
  }
}

/** Links only to http(s): plugin data must not inject javascript: URLs. */
export function safeHref(value: unknown): string | undefined {
  if (typeof value !== 'string') return undefined
  try {
    const u = new URL(value)
    return u.protocol === 'https:' || u.protocol === 'http:' ? u.href : undefined
  } catch {
    return undefined
  }
}

function list(structured: unknown, path: string | undefined): unknown[] {
  const v = valueAt(structured, path)
  return Array.isArray(v) ? v : []
}

export interface TableModel {
  headers: string[]
  rows: Cell[][]
}

export function tableModel(spec: ToolViewSpec, structured: unknown, locale: string): TableModel {
  const columns = spec.columns ?? []
  return {
    headers: columns.map((c) => localizedText(c.title, locale) || c.field),
    rows: list(structured, spec.items).map((item) =>
      columns.map((c) => ({ text: display(valueAt(item, c.field)), href: safeHref(valueAt(item, c.link)) })),
    ),
  }
}

export interface CardModel {
  title: string
  subtitle: string
  body: string
  href?: string
}

export function cardsModel(spec: ToolViewSpec, structured: unknown): CardModel[] {
  return list(structured, spec.items).map((item) => ({
    title: display(valueAt(item, spec.title)),
    subtitle: spec.subtitle ? display(valueAt(item, spec.subtitle)) : '',
    body: spec.body ? display(valueAt(item, spec.body)) : '',
    href: safeHref(valueAt(item, spec.link)),
  }))
}

export interface KVModel {
  title: string
  href?: string
  rows: Array<{ label: string } & Cell>
}

export function kvModel(spec: ToolViewSpec, structured: unknown, locale: string): KVModel {
  const obj = valueAt(structured, spec.items)
  const record = typeof obj === 'object' && obj !== null && !Array.isArray(obj) ? (obj as Record<string, unknown>) : {}
  const fields: ToolViewColumn[] = spec.fields?.length
    ? spec.fields
    : Object.keys(record)
        .filter((k) => k !== spec.title && k !== spec.link)
        .map((field) => ({ field }))
  return {
    title: spec.title ? display(valueAt(record, spec.title)) : '',
    href: safeHref(valueAt(record, spec.link)),
    rows: fields
      .map((f) => ({
        label: localizedText(f.title, locale) || f.field,
        text: display(valueAt(record, f.field)),
        href: safeHref(valueAt(record, f.link)),
      }))
      .filter((r) => r.text !== ''),
  }
}

/** The Markdown a markdown view shows: its field, else the text for the model. */
export function markdownSource(spec: ToolViewSpec, structured: unknown, output: string): string {
  const v = spec.field ? valueAt(structured, spec.field) : undefined
  return typeof v === 'string' ? v : output
}

/**
 * The arguments the tool was called with. Through the call_mcp_tool proxy
 * the real arguments sit under "arguments".
 */
export function toolArguments(args: Record<string, unknown> | undefined): Record<string, unknown> {
  const inner = args?.arguments
  if (inner && typeof inner === 'object' && !Array.isArray(inner)) return inner as Record<string, unknown>
  return args ?? {}
}

export interface PluginToolViewData {
  plugin_view?: ToolViewSpec
  plugin_id?: string
  plugin_version?: string
  mcp_server?: string
  mcp_tool?: string
  structured?: unknown
}
