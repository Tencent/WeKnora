import type { ModelConfig } from '@/api/model'

/**
 * 'vision' 是一个伪类型：图像理解槽位（KB 图片描述、智能体看图）选择的不是
 * 一种模型类型，而是声明了图片输入的对话模型（ADR 0004）。
 */
export type ModelSelectorType = ModelConfig['type'] | 'vision'

export function filterModelsByType(
  allModels: ModelConfig[],
  modelType: ModelSelectorType,
): ModelConfig[] {
  if (modelType === 'vision') {
    return allModels.filter(
      (m) => m.type === 'KnowledgeQA' && m.parameters?.supports_vision === true,
    )
  }

  return allModels.filter((m) => m.type === modelType)
}
