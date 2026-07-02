import { get, post } from '@/utils/request'

// SubmitFeedbackRequest 提交反馈请求
export interface SubmitFeedbackRequest {
  message_id: string
  is_positive: boolean
  dislike_reason?: string
}

// ChunkStatsResponse 片段统计响应
export interface ChunkStatsResponse {
  chunk_id: string
  like_count: number
  dislike_count: number
  positive_rate: number
  recall_weight: number
  quality_status: string
  related_session_count: number
  dislike_reasons: string[]
  last_feedback_at?: string
}

// ChunkQualityStats 低质量片段统计
export interface ChunkQualityStats {
  chunk_id: string
  knowledge_id: string
  knowledge_name?: string
  content: string
  like_count: number
  dislike_count: number
  positive_rate: number
  recall_weight: number
  quality_status: string
  updated_at: string
}

// WeightLogItem 权重变更日志项
export interface WeightLogItem {
  id: string
  chunk_id: string
  action: string
  old_weight: number
  new_weight: number
  trigger_type: string
  trigger_detail?: string
  operator?: string
  created_at: string
}

// WeightLogResponse 权重日志响应
export interface WeightLogResponse {
  logs: WeightLogItem[]
  total: number
}

// ListLowQualityChunksRequest 低质量片段列表请求
export interface ListLowQualityChunksRequest {
  max_rate?: number
  limit?: number
  offset?: number
}

// 提交问答反馈
export function submitFeedback(data: SubmitFeedbackRequest) {
  return post('/api/v1/feedback', data)
}

// 获取点踩原因选项
export function getDislikeReasons() {
  return get<string[]>('/api/v1/feedback/dislike-reasons')
}

// 获取片段统计
export function getChunkStats(chunkId: string) {
  return get<{ success: boolean; data: ChunkStatsResponse }>(`/api/v1/chunks/${chunkId}/stats`)
}

// 获取低质量片段列表
export function listLowQualityChunks(params?: ListLowQualityChunksRequest) {
  return get<{ success: boolean; data: ChunkQualityStats[] }>('/api/v1/chunks/low-quality', { params })
}

// 重置片段反馈（管理员）
export function resetChunkFeedback(chunkId: string) {
  return post<{ success: boolean; message: string }>(`/api/v1/admin/chunks/${chunkId}/reset-feedback`, {})
}

// 获取权重变更日志（管理员）
export function getChunkWeightLogs(chunkId: string, limit?: number) {
  return get<{ success: boolean; data: WeightLogResponse }>(
    `/api/v1/admin/chunks/${chunkId}/weight-logs`,
    { params: { limit } }
  )
}
