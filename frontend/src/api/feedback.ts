import { get, post } from '../utils/request'

export interface ChunkFeedbackListItem {
  chunk_id: string
  knowledge_id: string
  knowledge_base_id?: string
  knowledge_title: string
  chunk_index: number
  chunk_type: string
  content_preview: string
  like_count: number
  dislike_count: number
  session_count: number
  positive_rate: number | null
  stored_recall_weight: number
  effective_recall_weight: number
  needs_optimization: boolean
  feedback_reset_at?: string | null
  updated_at: string
}

export interface ChunkFeedbackAudit {
  id: number
  action: 'feedback_weight_changed' | 'feedback_reset'
  trigger_source: 'like' | 'dislike' | 'cancel' | 'admin_reset' | 'content_delete' | 'legacy'
  old_weight: number
  new_weight: number
  created_at: string
}

export interface ChunkFeedbackDetail extends ChunkFeedbackListItem {
  content: string
  reason_counts: Record<string, number>
  audits: ChunkFeedbackAudit[]
}

export interface PageResult<T> {
  total: number
  page: number
  page_size: number
  data: T[]
}

export interface ChunkFeedbackListParams {
  page?: number
  page_size?: number
  keyword?: string
  feedback_status?: 'all' | 'rated' | 'high' | 'normal' | 'low' | 'unrated'
  needs_optimization?: boolean
  sort_by?: 'updated_at' | 'like_count' | 'dislike_count' | 'positive_rate'
    | 'stored_recall_weight' | 'effective_recall_weight' | 'chunk_index'
  sort_order?: 'asc' | 'desc'
}

interface APIResponse<T> {
  success: boolean
  data: T
}

const encodeID = (value: string) => encodeURIComponent(value)

export function listChunkFeedback(kbId: string, params: ChunkFeedbackListParams) {
  const query = new URLSearchParams()
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== '') query.set(key, String(value))
  })
  const suffix = query.size ? `?${query.toString()}` : ''
  return get<APIResponse<PageResult<ChunkFeedbackListItem>>>(
    `/api/v1/knowledge-bases/${encodeID(kbId)}/chunk-feedback${suffix}`,
  )
}

export function getChunkFeedbackDetail(kbId: string, chunkId: string) {
  return get<APIResponse<ChunkFeedbackDetail>>(
    `/api/v1/knowledge-bases/${encodeID(kbId)}/chunk-feedback/${encodeID(chunkId)}`,
  )
}

export function listChunkFeedbackHistory(kbId: string, chunkId: string, page = 1, pageSize = 20) {
  return get<APIResponse<PageResult<ChunkFeedbackAudit>>>(
    `/api/v1/knowledge-bases/${encodeID(kbId)}/chunk-feedback/${encodeID(chunkId)}/history`
      + `?page=${page}&page_size=${pageSize}`,
  )
}

export function resetChunkFeedbackGovernance(kbId: string, chunkId: string) {
  return post<void>(
    `/api/v1/knowledge-bases/${encodeID(kbId)}/chunk-feedback/${encodeID(chunkId)}/reset`,
    {},
  )
}
