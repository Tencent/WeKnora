import { get, del } from '@/utils/request'

export interface ChunkFeedbackStat {
  chunk_id: string
  knowledge_id: string
  knowledge_title: string
  content_preview: string
  like_count: number
  dislike_count: number
  total: number
  positive_rate: number
  recall_weight: number
  needs_optimization: boolean
  dislike_reasons: Record<string, number> | null
  session_count: number
}

export interface ChunkWeightLog {
  id: number
  knowledge_base_id: string
  chunk_id: string
  old_weight: number
  new_weight: number
  positive_rate: number
  trigger_source: 'feedback' | 'reset' | 'config'
  feedback_id?: string
  created_at: string
}

export interface ChunkFeedbackStatsQuery {
  page?: number
  page_size?: number
  sort_by?: 'like_count' | 'dislike_count' | 'positive_rate' | 'recall_weight' | 'total'
  order?: 'asc' | 'desc'
  min_total?: number
  min_rate?: number
  max_rate?: number
  needs_optimization?: boolean
}

export function getChunkFeedbackStats(kbId: string, query: ChunkFeedbackStatsQuery = {}) {
  const params = new URLSearchParams()
  Object.entries(query).forEach(([key, value]) => {
    if (value !== undefined && value !== null && value !== '') {
      params.set(key, String(value))
    }
  })
  const qs = params.toString()
  return get(`/api/v1/knowledge-bases/${kbId}/feedback/stats${qs ? `?${qs}` : ''}`)
}

export function getChunkWeightLogs(
  kbId: string,
  query: { chunk_id?: string; page?: number; page_size?: number } = {},
) {
  const params = new URLSearchParams()
  Object.entries(query).forEach(([key, value]) => {
    if (value !== undefined && value !== null && value !== '') {
      params.set(key, String(value))
    }
  })
  const qs = params.toString()
  return get(`/api/v1/knowledge-bases/${kbId}/feedback/weight-logs${qs ? `?${qs}` : ''}`)
}

export function resetKbFeedback(kbId: string) {
  return del(`/api/v1/knowledge-bases/${kbId}/feedback`)
}
