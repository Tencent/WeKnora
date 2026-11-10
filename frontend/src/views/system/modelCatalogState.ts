import type { CatalogModel, CatalogOverlay, CatalogProvider } from '@/api/system/modelCatalog'

export const modelIdentity = (model: CatalogModel) => `${model.type || 'KnowledgeQA'}:${model.id || model.match || ''}`
export interface CatalogRow { key: string; provider: string; model: CatalogModel }
export function catalogRows(providers: CatalogProvider[]): CatalogRow[] {
  return providers.flatMap(provider => (provider.models || []).map(model => ({
    key: `${provider.id}:${modelIdentity(model)}`, provider: provider.id, model,
  })))
}
export function stableJSON(value: unknown): string {
  return JSON.stringify(value, (_, v) => v && typeof v === 'object' && !Array.isArray(v)
    ? Object.fromEntries(Object.entries(v).sort(([a], [b]) => a.localeCompare(b))) : v, 2)
}
export function catalogChanges(before: CatalogProvider[], after: CatalogProvider[]) {
  const flatten = (providers: CatalogProvider[]) => new Map<string, unknown>(providers.flatMap(p => [
    [p.id, { name: p.name, api: p.api, model_types: p.model_types, settings: p.settings }] as const,
    ...catalogRows([p]).map(row => [row.key, row.model] as const),
  ]))
  const old = flatten(before), next = flatten(after)
  return [...new Set([...old.keys(), ...next.keys()])].sort().flatMap(key => {
    const a = old.get(key), b = next.get(key)
    return stableJSON(a) === stableJSON(b) ? [] : [{ key, before: a, after: b }]
  })
}

// Overlays are plain JSON; a JSON round trip also unwraps Vue reactive
// proxies, which structuredClone rejects.
const cloneOverlay = (overlay: CatalogOverlay): CatalogOverlay => JSON.parse(JSON.stringify(overlay))

// Both overlay forms are supported. A form edit must also remove the same
// fields from model_overrides, which otherwise wins later during resolution.
export function patchCatalogModel(overlay: CatalogOverlay, provider: string, model: CatalogModel, patch: Record<string, unknown>) {
  const next = cloneOverlay(overlay)
  const p = next.providers[provider] ||= {}
  const entries: CatalogModel[] = p.models ||= []
  const type = model.type || 'KnowledgeQA'
  let entry = entries.find(m => m.id.toLowerCase() === model.id.toLowerCase() && (!m.type || m.type === type))
  if (!entry) { entry = { id: model.id, type }; entries.push(entry) }
  for (const [field, value] of Object.entries(patch)) {
    if (value === undefined) delete entry[field]
    else entry[field] = value
    for (const [id, override] of Object.entries(p.model_overrides || {})) {
      if (id.toLowerCase() === model.id.toLowerCase()) delete (override as Record<string, unknown>)[field]
    }
  }
  p.models = entries.filter(m => Object.keys(m).some(k => k !== 'id' && k !== 'type'))
  return next
}

export function removeCatalogModelOverride(overlay: CatalogOverlay, provider: string, model: CatalogModel) {
  const next = cloneOverlay(overlay)
  const p = next.providers[provider]
  if (!p) return next
  p.models = (p.models || []).filter((m: CatalogModel) => !(m.id.toLowerCase() === model.id.toLowerCase() && (!m.type || m.type === (model.type || 'KnowledgeQA'))))
  for (const id of Object.keys(p.model_overrides || {})) {
    if (id.toLowerCase() === model.id.toLowerCase()) delete p.model_overrides[id]
  }
  return next
}

// True when the console overlay mentions this model in either overlay form.
export function hasModelOverride(overlay: CatalogOverlay | undefined, provider: string, model: CatalogModel) {
  const p = overlay?.providers[provider]
  if (!p || !model.id) return false
  const id = model.id.toLowerCase(), type = model.type || 'KnowledgeQA'
  return (p.models || []).some((m: CatalogModel) => m.id?.toLowerCase() === id && (!m.type || m.type === type))
    || Object.keys(p.model_overrides || {}).some(key => key.toLowerCase() === id)
}

export type CatalogSource = 'console' | 'deployment' | 'builtin'
// The layer that last changed the effective entry; admins care about their
// own overrides first, then whether a deployment file shadows the built-in.
export function catalogSource(overlay: CatalogOverlay | undefined, row: CatalogRow,
  deployment: CatalogModel | undefined, builtin: CatalogModel | undefined): CatalogSource {
  if (hasModelOverride(overlay, row.provider, row.model) || stableJSON(deployment) !== stableJSON(row.model)) return 'console'
  return stableJSON(builtin) !== stableJSON(row.model) ? 'deployment' : 'builtin'
}

// Token counts are usually powers of two or round thousands; show them the
// way vendors publish them (128K, 1M) and fall back to grouped digits.
export function formatTokens(value?: number) {
  if (!value) return ''
  for (const [unit, size] of [['M', 1000000], ['M', 1048576], ['K', 1000], ['K', 1024]] as const) {
    if (value >= size && value % size === 0) return `${value / size}${unit}`
  }
  return value.toLocaleString('en-US')
}

export interface CatalogChangeSummary { key: string; provider: string; target: string; kind: 'added' | 'removed' | 'updated' | 'provider'; fields: string[] }
export function summarizeChanges(changes: ReturnType<typeof catalogChanges>): CatalogChangeSummary[] {
  return changes.map(({ key, before, after }) => {
    const [provider = key, type, ...id] = key.split(':')
    const fields = [...new Set([...Object.keys(before || {}), ...Object.keys(after || {})])].filter(field =>
      stableJSON((before as Record<string, unknown> | undefined)?.[field]) !== stableJSON((after as Record<string, unknown> | undefined)?.[field])).sort()
    if (!type) return { key, provider, target: provider, kind: 'provider', fields }
    const target = id.join(':') || type
    return { key, provider, target, kind: !before ? 'added' : !after ? 'removed' : 'updated', fields: before && after ? fields : [] }
  })
}

export type FieldKind = 'text' | 'tokens' | 'number' | 'bool' | 'modalities' | 'levels'

// Modalities the console edits; text is implied for chat models and any
// other value from the deployment file is kept as-is.
export const EDITABLE_INPUTS = ['image', 'audio', 'video'] as const
export function composeInput(current: string[] | undefined, extras: string[]) {
  const kept = (current?.length ? current : ['text']).filter(m => !(EDITABLE_INPUTS as readonly string[]).includes(m))
  return [...kept, ...EDITABLE_INPUTS.filter(m => extras.includes(m))]
}
export const sameMembers = (a: readonly string[] = [], b: readonly string[] = []) =>
  a.length === b.length && a.every(item => b.includes(item))

export const THINKING_LEVELS = ['off', 'auto', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'] as const

// Builds the model's thinking_levels overlay from the levels an admin wants.
// Keys agreeing with the inherited resolution are dropped so vendor changes
// keep flowing through; a re-enabled level reuses the vendor's wire value,
// and custom values written in the JSON editor survive while still enabled.
export function thinkingLevelsPatch(selected: readonly string[], inherited: readonly string[],
  current: Record<string, string | null> | undefined, vendorMap: Record<string, string | null> | undefined) {
  const out: Record<string, string | null> = {}
  for (const level of THINKING_LEVELS) {
    const want = selected.includes(level)
    if (want && typeof current?.[level] === 'string') out[level] = current[level]!
    else if (want !== inherited.includes(level)) out[level] = want ? (typeof vendorMap?.[level] === 'string' ? vendorMap[level]! : level) : null
  }
  return Object.keys(out).length ? out : undefined
}
