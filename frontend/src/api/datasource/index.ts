import { get, post, put, del } from '../../utils/request'

// --- Types ---

export interface DataSource {
  id: string
  tenant_id: number
  knowledge_base_id: string
  name: string
  type: string
  config: any
  sync_schedule: string
  sync_mode: 'incremental' | 'full'
  status: 'active' | 'paused' | 'error'
  conflict_strategy: 'overwrite' | 'skip'
  sync_deletions: boolean
  last_sync_at: string | null
  last_sync_result: any
  error_message: string
  created_at: string
  updated_at: string
  latest_sync_log?: SyncLog
}

export interface SyncLog {
  id: string
  data_source_id: string
  status: 'running' | 'success' | 'partial' | 'failed' | 'canceled'
  started_at: string
  finished_at: string | null
  items_total: number
  items_created: number
  items_updated: number
  items_deleted: number
  items_skipped: number
  items_failed: number
  error_message: string
}

export interface ConnectorMeta {
  type: string
  name: string
  description: string
  icon: string
  priority: number
  auth_type: string
  capabilities: string[]
}

export interface Resource {
  external_id: string
  name: string
  type: string
  description: string
  url: string
  parent_id?: string
  has_children?: boolean
}

// --- API calls ---

export function getConnectorTypes() {
  return get('/api/v1/datasource/types')
}

export function listDataSources(kbId: string) {
  return get(`/api/v1/datasource?kb_id=${encodeURIComponent(kbId)}`)
}

export function getDataSource(id: string) {
  return get(`/api/v1/datasource/${id}`)
}

export function createDataSource(data: Partial<DataSource>) {
  return post('/api/v1/datasource', data)
}

export function updateDataSource(id: string, data: Partial<DataSource>) {
  return put(`/api/v1/datasource/${id}`, data)
}

export function deleteDataSource(id: string) {
  return del(`/api/v1/datasource/${id}`)
}

export function validateConnection(id: string) {
  return post(`/api/v1/datasource/${id}/validate`, {})
}

// Validate credentials without persisting (for "Test Connection" during creation)
export function validateCredentials(type: string, credentials: Record<string, any>) {
  return post('/api/v1/datasource/validate-credentials', { type, credentials })
}

export function listResources(id: string) {
  return get(`/api/v1/datasource/${id}/resources`)
}

export function triggerSync(id: string) {
  return post(`/api/v1/datasource/${id}/sync`, {})
}

export function pauseDataSource(id: string) {
  return post(`/api/v1/datasource/${id}/pause`, {})
}

export function resumeDataSource(id: string) {
  return post(`/api/v1/datasource/${id}/resume`, {})
}

export function getSyncLogs(id: string, limit = 20, offset = 0) {
  return get(`/api/v1/datasource/${id}/logs?limit=${limit}&offset=${offset}`)
}
