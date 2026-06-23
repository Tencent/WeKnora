import { listModels, type ModelConfig } from '@/api/model'

export interface TenantModelReadiness {
  chatCount: number
  embeddingCount: number
  hasChat: boolean
  hasEmbedding: boolean
  /** 文档库默认开启向量/关键词检索时所需的模型是否齐备 */
  isReadyForDocumentKb: boolean
  /** 创建智能体至少需要对话模型 */
  isReadyForAgent: boolean
}

export function evaluateTenantModelReadiness(models: ModelConfig[]): TenantModelReadiness {
  const chatCount = models.filter((m) => m.type === 'KnowledgeQA').length
  const embeddingCount = models.filter((m) => m.type === 'Embedding').length
  const hasChat = chatCount > 0
  const hasEmbedding = embeddingCount > 0
  return {
    chatCount,
    embeddingCount,
    hasChat,
    hasEmbedding,
    isReadyForDocumentKb: hasChat && hasEmbedding,
    isReadyForAgent: hasChat,
  }
}

export async function fetchTenantModelReadiness(): Promise<TenantModelReadiness> {
  try {
    const models = await listModels()
    return evaluateTenantModelReadiness(models || [])
  } catch {
    return evaluateTenantModelReadiness([])
  }
}
