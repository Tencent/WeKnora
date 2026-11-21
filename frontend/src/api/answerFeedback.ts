import { get, post } from '@/utils/request'

export interface AnswerFeedbackState {
  rating: 'like' | 'dislike' | 'none'
  reason?: string
  chunk_count?: number
}

export function submitAnswerFeedback(
  sessionId: string,
  messageId: string,
  body: { rating: 'like' | 'dislike' | 'none'; reason?: string },
) {
  return post(`/api/v1/messages/${sessionId}/${messageId}/feedback`, body)
}

export function getAnswerFeedback(sessionId: string, messageId: string) {
  return get(`/api/v1/messages/${sessionId}/${messageId}/feedback`)
}

export interface ChunkFeedbackSummary {
  chunk_id: string
  knowledge_id: string
  knowledge_title: string
  content_preview: string
  like_count: number
  dislike_count: number
  approval_rate: number | null
  recall_weight: number
  needs_optimization: boolean
  session_count: number
}

export interface ChunkFeedbackPage {
  items: ChunkFeedbackSummary[]
  total: number
  page: number
  page_size: number
}

export interface ChunkFeedbackDetail extends ChunkFeedbackSummary {
  dislike_reasons: { reason: string; count: number }[]
  sessions: { session_id: string; message_id: string; created_at: string }[]
  weight_logs: {
    id: string
    old_weight: number
    new_weight: number
    old_approval_rate: number | null
    new_approval_rate: number | null
    like_count: number
    dislike_count: number
    trigger_source: string
    message_id?: string
    created_at: string
  }[]
}

export interface ChunkFeedbackQuery {
  page?: number
  page_size?: number
  max_approval_rate?: number
  low_quality?: boolean
  needs_optimization?: boolean
}

export function listChunkFeedback(knowledgeBaseId: string, query: ChunkFeedbackQuery = {}) {
  const params = new URLSearchParams()
  if (query.page) params.set('page', String(query.page))
  if (query.page_size) params.set('page_size', String(query.page_size))
  if (query.max_approval_rate != null && !Number.isNaN(query.max_approval_rate)) {
    params.set('max_approval_rate', String(query.max_approval_rate))
  }
  if (query.low_quality) params.set('low_quality', 'true')
  if (query.needs_optimization) params.set('needs_optimization', 'true')
  const qs = params.toString()
  return get(`/api/v1/knowledge-bases/${knowledgeBaseId}/chunk-feedback${qs ? `?${qs}` : ''}`)
}

export function getChunkFeedback(knowledgeBaseId: string, chunkId: string) {
  return get(`/api/v1/knowledge-bases/${knowledgeBaseId}/chunk-feedback/${chunkId}`)
}

export function resetChunkFeedback(knowledgeBaseId: string, chunkId: string) {
  return post(`/api/v1/knowledge-bases/${knowledgeBaseId}/chunk-feedback/${chunkId}/reset`, {})
}
