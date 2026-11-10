export interface ModelDefaultCandidate {
  id?: string
  type: string
  status?: string
  is_default?: boolean
  default_for?: string[]
  parameters?: { supports_vision?: boolean }
}

export function modelSupportsType(model: ModelDefaultCandidate, modelType: string): boolean {
  return model.type === modelType
    || (modelType === 'VLLM' && model.type === 'KnowledgeQA' && model.parameters?.supports_vision === true)
}

export function isDefaultModelForType(model: ModelDefaultCandidate, modelType: string): boolean {
  return Boolean(model.default_for?.includes(modelType) || (model.type === modelType && model.is_default))
}

/**
 * Pick a creation-time model: prefer the declared default, fall back to the
 * first active model.
 */
export function selectInitialModelId(
  models: readonly ModelDefaultCandidate[],
  modelType: string,
): string | null {
  const active = models.filter(
    model => Boolean(model.id?.trim())
      && modelSupportsType(model, modelType)
      && (!model.status || model.status === 'active'),
  )
  const chosen = active.find(model => model.default_for?.includes(modelType))
    ?? active.find(model => isDefaultModelForType(model, modelType))
    ?? active[0]
  return chosen?.id?.trim() ?? null
}
