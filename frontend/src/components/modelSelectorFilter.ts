import type { ModelConfig } from '@/api/model'

export type ModelSelectorType = ModelConfig['type']

export function filterModelsByType(
  allModels: ModelConfig[],
  modelType: ModelSelectorType,
): ModelConfig[] {
  // A video model is its own row type: unlike VLM (which is served by chat rows
  // that accept images) it is not selectable from another type's rows.
  if (modelType === 'VLLM') {
    return allModels.filter(
      (m) =>
        m.type === 'VLLM' ||
        (m.type === 'KnowledgeQA' && m.parameters?.supports_vision === true),
    )
  }

  return allModels.filter((m) => m.type === modelType)
}
