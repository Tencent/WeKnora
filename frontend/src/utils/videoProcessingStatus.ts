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
  if (code === 'llm_connection_closed') return 'AI 服务连接提前关闭，系统将按任务重试策略处理'
  if (code === 'llm_stream_incomplete') return 'AI 返回内容不完整，旧总结已保留，请重试智能总结'
  if (code === 'llm_deadline_exceeded' || (code.startsWith('llm_stream_') && code.endsWith('_timeout'))) return 'AI 生成超时，旧总结已保留，请重试智能总结'
  if (code === 'summary_contract_invalid') return 'AI 总结未通过内容契约，旧总结已保留，请重试智能总结'
  return message || '可重试当前阶段'
}
