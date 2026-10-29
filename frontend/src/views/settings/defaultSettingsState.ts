import { isDefaultModelForType, modelSupportsType, type ModelDefaultCandidate } from '../../utils/modelDefaults'

export type DefaultModelType = 'KnowledgeQA' | 'Embedding' | 'Rerank' | 'VLLM' | 'ASR'

export type DefaultModelLike = ModelDefaultCandidate

// workspace: the slot's own choice. system: the deployment default (a
// built-in model's is_default). auto: nothing chosen, the first usable model.
// chat: the utility slot following the chat default.
export type DefaultSource = 'workspace' | 'system' | 'auto' | 'chat' | 'none'

export interface EffectiveDefault<M extends DefaultModelLike> {
  model: M | null
  source: DefaultSource
  // The slot names a model that is gone, inactive or of another type.
  stale: boolean
}

function usable<M extends DefaultModelLike>(model: M, type: DefaultModelType) {
  return Boolean(model.id?.trim()) && modelSupportsType(model, type) && (!model.status || model.status === 'active')
}

// Mirrors the backend: the model list already marks the workspace choice as
// is_default when it is usable, so the fallback only runs for empty or stale
// slots, where is_default is the deployment default.
export function effectiveModelDefault<M extends DefaultModelLike>(
  models: M[],
  type: DefaultModelType,
  configuredId: string,
): EffectiveDefault<M> {
  const id = configuredId.trim()
  const chosen = id ? models.find((m) => m.id === id && usable(m, type)) : undefined
  if (chosen) return { model: chosen, source: 'workspace', stale: false }
  const stale = id !== ''
  const candidates = models.filter((m) => usable(m, type))
  const flagged = candidates.find((m) => m.default_for?.includes(type))
    ?? candidates.find((m) => isDefaultModelForType(m, type))
  if (flagged) return { model: flagged, source: 'system', stale }
  if (candidates[0]) return { model: candidates[0], source: 'auto', stale }
  return { model: null, source: 'none', stale }
}

export function effectiveUtilityDefault<M extends DefaultModelLike>(
  models: M[],
  utilityId: string,
  chatId: string,
): EffectiveDefault<M> {
  const own = effectiveModelDefault(models, 'KnowledgeQA', utilityId)
  if (own.source === 'workspace') return own
  const chat = effectiveModelDefault(models, 'KnowledgeQA', chatId)
  return { model: chat.model, source: chat.model ? 'chat' : 'none', stale: own.stale }
}
