import { get, post, put, del } from '@/utils/request'

export interface StorageBackendConfig {
  mode?: string
  endpoint?: string
  region?: string
  access_key_id?: string
  secret_access_key?: string
  bucket_name?: string
  path_prefix?: string
  app_id?: string
  use_ssl?: boolean
  force_path_style?: boolean
  use_temp_bucket?: boolean
  temp_bucket_name?: string
  temp_region?: string
}

export interface StorageBackend {
  id: string
  tenant_id?: number
  name: string
  provider: string
  config: StorageBackendConfig
  source: 'user' | 'env'
  status: 'active' | 'disabled'
  legacy_alias?: boolean
  created_at?: string
  updated_at?: string
}

export interface StorageBackendListResponse {
  success: boolean
  data: StorageBackend[]
  default_storage_backend_id?: string | null
}

export const listStorageBackends = (): Promise<StorageBackendListResponse> => get('/api/v1/storage-backends')
export const listStorageBackendTypes = (): Promise<{ success: boolean; data: string[] }> => get('/api/v1/storage-backends/types')
export const createStorageBackend = (data: Partial<StorageBackend>) => post('/api/v1/storage-backends', data)
export const updateStorageBackend = (id: string, data: Partial<StorageBackend>) => put(`/api/v1/storage-backends/${id}`, data)
export const deleteStorageBackend = (id: string) => del(`/api/v1/storage-backends/${id}`)
export const setDefaultStorageBackend = (id: string) => put(`/api/v1/storage-backends/${id}/default`, {})
export const testStorageBackend = (data: Partial<StorageBackend>) => post('/api/v1/storage-backends/test', data)
export const testStorageBackendByID = (id: string) => post(`/api/v1/storage-backends/${id}/test`, {})
