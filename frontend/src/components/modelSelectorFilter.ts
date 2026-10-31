import type { ModelConfig } from '@/api/model'
import { modelSupportsType } from '../utils/modelDefaults'

export type ModelSelectorType = ModelConfig['type']

export function filterModelsByType(
  allModels: ModelConfig[],
  modelType: ModelSelectorType,
): ModelConfig[] {
  return allModels.filter((m) => modelSupportsType(m, modelType))
}
