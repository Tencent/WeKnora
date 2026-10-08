import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import i18n from '@/i18n'
import { listModelProviders, type ModelProviderOption } from '@/api/initialization'
import {
  loadProvidersForType,
  mergeProviderIndex,
  providerDescription,
  providerIcon,
  providerLabel,
} from '@/stores/modelProvidersState'

/**
 * Vendor catalog cache. Every component that renders a vendor (model
 * editor, model cards, debug drawer) reads through this store so the
 * frontend keeps no vendor table or naming heuristics of its own — the
 * backend catalog (GET /api/v1/models/providers) is the single source.
 *
 * Providers are fetched once per model type (the backend filters the
 * built-in model list by type) and merged into an id index so lookups by
 * provider id work regardless of which type loaded them first.
 */
export const useModelProvidersStore = defineStore('modelProviders', () => {
  const byType = ref<Record<string, ModelProviderOption[]>>({})
  const byId = ref<Record<string, ModelProviderOption>>({})
  const loadingByType = ref<Record<string, boolean>>({})
  let generation = 0
  const requestTokens = new Map<string, symbol>()
  const pending = new Map<string, Promise<ModelProviderOption[]>>()

  const currentLocale = computed(() => String(i18n.global.locale.value || ''))

  const normalizeType = (modelType?: string) => (modelType || '').trim().toLowerCase()

  /**
   * Load providers for one model type (once); concurrent callers share the
   * request. Never rejects: a failed fetch resolves to [] and is NOT cached,
   * so the next component to mount retries instead of rendering an empty
   * vendor dropdown for the rest of the session.
   */
  const ensureLoaded = async (modelType?: string, force = false): Promise<ModelProviderOption[]> => {
    const key = normalizeType(modelType)
    if (!force && byType.value[key]) return byType.value[key]
    const inflight = pending.get(key)
    if (inflight && !force) return inflight

    const requestGeneration = generation
    const token = Symbol(key)
    requestTokens.set(key, token)
    const request = (async () => {
      loadingByType.value = { ...loadingByType.value, [key]: true }
      try {
        const { providers, cacheable } = await loadProvidersForType(listModelProviders, key)
        if (!cacheable || requestGeneration !== generation || requestTokens.get(key) !== token) return providers
        byType.value = { ...byType.value, [key]: providers }
        // The freshest list is merged first so its metadata wins over older types.
        byId.value = [providers, ...Object.entries(byType.value).filter(([type]) => type !== key).map(([, entries]) => entries)]
          .reduce((index, entries) => mergeProviderIndex(index, entries), {} as Record<string, ModelProviderOption>)
        return providers
      } finally {
        if (requestGeneration === generation && requestTokens.get(key) === token) {
          loadingByType.value = { ...loadingByType.value, [key]: false }
        }
      }
    })()
    pending.set(key, request)
    // Only drop our own entry: a concurrent force-reload may have replaced it.
    void request.finally(() => {
      if (pending.get(key) === request) pending.delete(key)
    })
    return request
  }

  const providersFor = (modelType?: string): ModelProviderOption[] => byType.value[normalizeType(modelType)] || []

  const isLoading = (modelType?: string): boolean => !!loadingByType.value[normalizeType(modelType)]

  const providerById = (id?: string | null): ModelProviderOption | undefined => {
    if (!id) return undefined
    return byId.value[id]
  }

  const iconFor = (id?: string | null): string => providerIcon(providerById(id))

  const labelFor = (id?: string | null, locale?: string): string => {
    const provider = providerById(id)
    if (!provider) return id || ''
    return providerLabel(provider, locale ?? currentLocale.value)
  }

  const descriptionFor = (id?: string | null, locale?: string): string =>
    providerDescription(providerById(id), locale ?? currentLocale.value)

  const reset = () => {
    generation++
    requestTokens.clear()
    byType.value = {}
    byId.value = {}
    loadingByType.value = {}
    pending.clear()
  }

  return {
    byType,
    byId,
    currentLocale,
    ensureLoaded,
    providersFor,
    isLoading,
    providerById,
    iconFor,
    labelFor,
    descriptionFor,
    reset,
  }
})
