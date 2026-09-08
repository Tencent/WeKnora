import type { VideoProcessingFailure } from '@/types/videohub'

export const KNOWLEDGE_CONTRACT_FAILURE_MESSAGE = 'AI 契约校验失败：输出未通过知识契约，未写入知识图谱。请重试知识提取'
export const SUPPORTED_KNOWLEDGE_CONTRACT_VERSION = 'p3-wiki-object/v1'

export function assertSupportedKnowledgeContract(version: string): void {
  if (version !== SUPPORTED_KNOWLEDGE_CONTRACT_VERSION) {
    throw new Error('知识处理契约版本不兼容，请刷新页面或联系管理员')
  }
}

export function resolveProcessingFailureMessage(failure?: VideoProcessingFailure): string {
  if (!failure) return '可重试当前阶段'
  return resolveProcessingErrorMessage(failure.code, failure.message)
}

export function resolveProcessingErrorMessage(code: string, message = ''): string {
  if (code === 'content_contract_failed') return KNOWLEDGE_CONTRACT_FAILURE_MESSAGE
  return message || '可重试当前阶段'
}
