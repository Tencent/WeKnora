import { get } from '@/utils/request'
import type {
  ProcessingDashboardResponse,
  ProcessingKnowledgeDetailResponse,
  ProcessingLogicalStage,
  ProcessingStageItemsResponse,
  ProcessingStageState,
} from '@/types/processingDashboard'

export interface ProcessingDashboardQuery {
  kb_id?: string
  keyword?: string
  active_limit?: number
}

export function getProcessingDashboard(params: ProcessingDashboardQuery = {}, signal?: AbortSignal) {
  const qs = new URLSearchParams()
  if (params.kb_id) qs.set('kb_id', params.kb_id)
  if (params.keyword) qs.set('keyword', params.keyword)
  if (params.active_limit) qs.set('active_limit', String(params.active_limit))
  const query = qs.toString()
  return get(query ? `/api/v1/knowledge-processing/dashboard?${query}` : '/api/v1/knowledge-processing/dashboard', { signal }) as unknown as Promise<{ success: boolean; data: ProcessingDashboardResponse }>
}

export function listProcessingStageItems(params: {
  stage: ProcessingLogicalStage
  state: Extract<ProcessingStageState, 'running' | 'queued' | 'retrying'>
  cursor?: string
  page_size?: number
  kb_id?: string
  keyword?: string
}, signal?: AbortSignal) {
  const qs = new URLSearchParams()
  qs.set('state', params.state)
  if (params.cursor) qs.set('cursor', params.cursor)
  if (params.page_size) qs.set('page_size', String(params.page_size))
  if (params.kb_id) qs.set('kb_id', params.kb_id)
  if (params.keyword) qs.set('keyword', params.keyword)
  return get(`/api/v1/knowledge-processing/stages/${params.stage}/items?${qs.toString()}`, { signal }) as unknown as Promise<{ success: boolean; data: ProcessingStageItemsResponse }>
}

export function getProcessingKnowledgeDetail(knowledgeId: string, attempt?: number, signal?: AbortSignal) {
  const qs = new URLSearchParams()
  if (attempt) qs.set('attempt', String(attempt))
  const query = qs.toString()
  return get(query ? `/api/v1/knowledge-processing/knowledge/${knowledgeId}?${query}` : `/api/v1/knowledge-processing/knowledge/${knowledgeId}`, { signal }) as unknown as Promise<{ success: boolean; data: ProcessingKnowledgeDetailResponse }>
}
