export interface ModelDefaultCandidate {
  id?: string
  type: string
  status?: string
  is_default?: boolean
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
      && model.type === modelType
      && (!model.status || model.status === 'active'),
  )
  return active.find(model => model.is_default)?.id?.trim() ?? active[0]?.id?.trim() ?? null
}
