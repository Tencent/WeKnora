import { del, get, post, put } from '@/utils/request'
import type { AgentCollectionField } from '@/api/agent'
import { getApiBaseUrl } from '@/utils/api-base'

export type CollectionValueEntry = {
  value: unknown
  updated_at?: string
  source?: string
}
export type AgentCollectionProfile = {
  id: string
  tenant_id: number
  agent_tenant_id: number
  agent_id: string
  agent_name: string
  user_id: string
  schema_version: number
  values: Record<string, CollectionValueEntry>
  inactive_values?: Record<string, CollectionValueEntry>
  fields: AgentCollectionField[]
  required_total: number
  completed_required: number
  is_complete: boolean
  created_at: string
  updated_at: string
}
export type CollectionFilter = {
  tenant_id?: number
  agent_id?: string
  user_id?: string
  keyword?: string
  complete?: boolean
  updated_from?: string
  updated_to?: string
  field_key?: string
  field_value?: string
  page?: number
  page_size?: number
}
export type CollectionPage = {
  items: AgentCollectionProfile[]
  total: number
  page: number
  page_size: number
  summary: { users: number; profiles: number; updated_today: number; incomplete: number }
}
export type CollectionHistory = {
  id: string
  field_key: string
  old_value?: unknown
  new_value?: unknown
  source: string
  actor_user_id?: string
  change_reason?: string
  created_at: string
}
export type CollectionExport = {
  id: string
  format: 'csv' | 'xlsx'
  status: 'pending' | 'running' | 'completed' | 'failed'
  filename?: string
  error_message?: string
}

const ROOT = '/api/v1/system/admin'

export const listCollectionProfiles = (filter: CollectionFilter) =>
  get<CollectionPage>(`${ROOT}/collection-profiles`, { params: filter })
export const getCollectionProfile = (id: string) =>
  get<AgentCollectionProfile>(`${ROOT}/collection-profiles/${id}`)
export const listCollectionHistory = (id: string, page = 1, pageSize = 20) =>
  get<{ items: CollectionHistory[]; total: number }>(`${ROOT}/collection-profiles/${id}/history`, { params: { page, page_size: pageSize } })
export const updateCollectionField = (profileId: string, fieldKey: string, value: unknown, reason: string) =>
  put(`${ROOT}/collection-profiles/${profileId}/fields/${encodeURIComponent(fieldKey)}`, { value, reason })
export const purgeCollectionProfile = (profileId: string, reason: string) =>
  del(`${ROOT}/collection-profiles/${profileId}`, { confirmation: profileId, reason })
export const createCollectionExport = (format: 'csv' | 'xlsx', filter: CollectionFilter) =>
  post<CollectionExport>(`${ROOT}/collection-profiles/export`, { format, filter })
export const getCollectionExport = (id: string) =>
  get<CollectionExport>(`${ROOT}/collection-exports/${id}`)

export async function downloadCollectionExport(id: string, filename?: string) {
  const token = localStorage.getItem('weknora_token')
  const response = await fetch(`${getApiBaseUrl()}${ROOT}/collection-exports/${id}?download=1`, {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  })
  if (!response.ok) throw new Error('下载导出文件失败')
  const url = URL.createObjectURL(await response.blob())
  const link = document.createElement('a')
  link.href = url
  link.download = filename || 'agent-collection-export'
  link.click()
  URL.revokeObjectURL(url)
}
