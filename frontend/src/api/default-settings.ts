import { get, put } from '@/utils/request'

// DefaultSettings is the workspace's default model choice per slot. An empty
// ID leaves the slot to the deployment default.
export interface DefaultSettings {
  chat_model_id: string
  utility_model_id: string
  embedding_model_id: string
  rerank_model_id: string
  vlm_model_id: string
  asr_model_id: string
}

export function getTenantDefaultSettings() {
  return get<{ success: boolean; data: DefaultSettings }>('/api/v1/tenants/kv/default-settings')
}

export function updateTenantDefaultSettings(settings: DefaultSettings) {
  return put<{ success: boolean; data: DefaultSettings }>('/api/v1/tenants/kv/default-settings', settings)
}
