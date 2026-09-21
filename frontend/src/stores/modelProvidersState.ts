/**
 * Pure helpers behind the modelProviders store. Kept free of Pinia / API
 * imports so they can be unit-tested under node:test.
 *
 * Every vendor fact the UI renders (name, description, icon, extra fields,
 * catalog models) comes from the backend catalog (internal/handler/
 * model_catalog.go); these helpers only pick the right locale / model type.
 */
import type {
  ModelProviderCredentialLabel,
  ModelProviderExtraField,
  ModelProviderOption,
} from '@/api/initialization'

/** Editor model types → backend ModelType names used by ExtraField.model_types. */
const FRONTEND_TO_BACKEND_MODEL_TYPE: Record<string, string> = {
  chat: 'KnowledgeQA',
  embedding: 'Embedding',
  rerank: 'Rerank',
  vllm: 'VLLM',
  asr: 'ASR',
}

const BACKEND_TO_FRONTEND_MODEL_TYPE: Record<string, string> = Object.fromEntries(
  Object.entries(FRONTEND_TO_BACKEND_MODEL_TYPE).map(([front, back]) => [back, front]),
)

/** Normalize either naming ("chat" / "KnowledgeQA") to the editor's lowercase form. */
export function normalizeModelType(type: string | null | undefined): string {
  if (!type) return ''
  const trimmed = String(type).trim()
  if (FRONTEND_TO_BACKEND_MODEL_TYPE[trimmed]) return trimmed
  return BACKEND_TO_FRONTEND_MODEL_TYPE[trimmed] || trimmed.toLowerCase()
}

/** Whether a field's model_types filter admits the given editor model type. */
export function extraFieldAppliesTo(field: Pick<ModelProviderExtraField, 'model_types'>, modelType: string): boolean {
  const restricted = field.model_types
  if (!restricted || restricted.length === 0) return true
  const wanted = normalizeModelType(modelType)
  return restricted.some((entry) => normalizeModelType(entry) === wanted)
}

/** Extra fields to render for a provider at one model type. */
export function extraFieldsForModelType(
  fields: ReadonlyArray<ModelProviderExtraField> | null | undefined,
  modelType: string,
): ModelProviderExtraField[] {
  if (!fields || fields.length === 0) return []
  return fields.filter((field) => !!field?.key && extraFieldAppliesTo(field, modelType))
}

/**
 * The credential naming to use at one model type, or null when the vendor
 * takes a plain API key. First match wins, and an entry without a
 * `model_types` filter applies everywhere — same rule as extra fields.
 */
export function credentialLabelForModelType(
  labels: ReadonlyArray<ModelProviderCredentialLabel> | null | undefined,
  modelType: string,
): ModelProviderCredentialLabel | null {
  if (!labels || labels.length === 0) return null
  return labels.find((label) => !!label?.label && extraFieldAppliesTo(label, modelType)) || null
}

/**
 * Pick a localized string from a `labels`-style map.
 *
 * Order: exact locale (`zh-CN`) → same language with any region (`zh-*`) →
 * the plain fallback. Empty strings never win.
 */
export function pickLocalized(
  localized: Record<string, string> | null | undefined,
  locale: string | null | undefined,
  fallback: string,
): string {
  if (localized && locale) {
    const exact = localized[locale]
    if (typeof exact === 'string' && exact.trim()) return exact
    const language = locale.split(/[-_]/)[0]?.toLowerCase()
    if (language) {
      for (const [key, value] of Object.entries(localized)) {
        if (key.split(/[-_]/)[0]?.toLowerCase() === language && typeof value === 'string' && value.trim()) {
          return value
        }
      }
    }
  }
  return fallback
}

export function providerLabel(provider: ModelProviderOption | null | undefined, locale?: string): string {
  if (!provider) return ''
  return pickLocalized(provider.labels, locale, provider.label || provider.value || '')
}

export function providerDescription(provider: ModelProviderOption | null | undefined, locale?: string): string {
  if (!provider) return ''
  return pickLocalized(provider.descriptions, locale, provider.description || '')
}

/** `<img src>`-ready icon, or '' when the vendor ships none. */
export function providerIcon(provider: ModelProviderOption | null | undefined): string {
  const icon = provider?.icon
  return typeof icon === 'string' && icon.startsWith('data:image/') ? icon : ''
}

export function extraFieldLabel(field: ModelProviderExtraField, locale?: string): string {
  return pickLocalized(field.labels, locale, field.label || field.key)
}

/** Stable display order: backend `order`, then label. */
export function sortProviders(providers: ReadonlyArray<ModelProviderOption>): ModelProviderOption[] {
  return [...providers].sort((a, b) => {
    const orderA = typeof a.order === 'number' ? a.order : Number.MAX_SAFE_INTEGER
    const orderB = typeof b.order === 'number' ? b.order : Number.MAX_SAFE_INTEGER
    if (orderA !== orderB) return orderA - orderB
    return String(a.label || a.value || '').localeCompare(String(b.label || b.value || ''))
  })
}

/**
 * Fetch + normalize the vendor list for one model type.
 *
 * `cacheable` is false for a failed or malformed response so the caller keeps
 * no cache entry and the next component to mount retries; caching a transient
 * failure would blank the vendor dropdown for the rest of the session.
 * An empty-but-valid list IS cacheable — that is a real answer.
 */
export async function loadProvidersForType(
  fetcher: (modelType?: string) => Promise<unknown>,
  modelType: string,
): Promise<{ providers: ModelProviderOption[]; cacheable: boolean }> {
  try {
    const raw = await fetcher(modelType || undefined)
    if (!Array.isArray(raw)) return { providers: [], cacheable: false }
    const providers = (raw as ModelProviderOption[]).filter((p) => !!p && typeof p.value === 'string' && !!p.value)
    return { providers: sortProviders(providers), cacheable: true }
  } catch (error) {
    console.error('Failed to load model providers', error)
    return { providers: [], cacheable: false }
  }
}

/** Merge a freshly fetched list into the id index without dropping models seen for other types. */
export function mergeProviderIndex(
  index: Record<string, ModelProviderOption>,
  providers: ReadonlyArray<ModelProviderOption>,
): Record<string, ModelProviderOption> {
  const next = { ...index }
  for (const provider of providers) {
    if (!provider?.value) continue
    const existing = next[provider.value]
    if (!existing) {
      next[provider.value] = provider
      continue
    }
    const seen = new Set((existing.models || []).filter((model) => !!model?.id).map((model) => model.id))
    const mergedModels = [
      ...(existing.models || []),
      ...(provider.models || []).filter((model) => !!model?.id && !seen.has(model.id)),
    ]
    next[provider.value] = { ...existing, ...provider, models: mergedModels }
  }
  return next
}
