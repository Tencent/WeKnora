import { get, post } from '@/utils/request'

export type DocumentProgressStatus = 'all' | 'unfinished' | 'failed' | 'completed'
export interface DocumentStageProgress {
  name: string
  status: string
  error_code?: string
  finished_at?: string
}
export interface DocumentProgress {
  id: string
  tenant_id: number
  knowledge_base_id: string
  knowledge_base_name: string
  title: string
  parse_status: string
  ingestion_status: string
  attempt: number
  wiki_status: string
  wiki_pending: number
  wiki_retries: number
  failure_count: number
  can_start: boolean
  stages: DocumentStageProgress[]
  updated_at: string
}
export interface DocumentProgressPage {
  documents: DocumentProgress[]
  total: number
  page: number
  page_size: number
}
export interface DocumentProgressDetail {
  document: DocumentProgress
  failures: Array<{ id: number; task_type: string; last_error: string; fail_count: number; failed_at: string }>
  has_more_failures: boolean
}
export interface WikiStartRequest {
  count: number
  knowledge_id?: string
  knowledge_base_id?: string
  search?: string
}
export interface WikiStartResult {
  requested: number
  started: number
  already_queued: number
}

const endpoint = '/api/v1/system/admin/runtime/documents'
export function listDocumentProgress(params: {
  page: number; page_size: number; status: DocumentProgressStatus; search?: string; kb_id?: string
}): Promise<{ data: DocumentProgressPage }> {
  return get(endpoint, { params })
}
export function getDocumentProgress(id: string): Promise<{ data: DocumentProgressDetail }> {
  return get(`${endpoint}/${encodeURIComponent(id)}`)
}
export function startDocumentWiki(request: WikiStartRequest): Promise<{ data: WikiStartResult }> {
  return post(`${endpoint}/wiki-start`, request)
}
