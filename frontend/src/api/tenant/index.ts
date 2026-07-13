import { del, get, post, put } from '@/utils/request'
import i18n from '@/i18n'

const t = (key: string) => i18n.global.t(key)

// 租户信息接口
export interface TenantInfo {
  id: number
  name: string
  description?: string
  status?: string
  business?: string
  storage_quota?: number
  storage_used?: number
  created_at: string
  updated_at: string
}

export type APIPrincipalMode = 'tenant' | 'direct_header' | 'signed_token'

export interface APIPrincipalConfig {
  mode: APIPrincipalMode
  direct_header_name: string
  signed_token_header_name: string
  require_direct_header: boolean
  // The server never returns the plaintext secret; only its presence.
  has_hmac_secret: boolean
}

export interface UpdateAPIPrincipalConfigPayload {
  mode: APIPrincipalMode
  direct_header_name?: string
  signed_token_header_name?: string
  require_direct_header?: boolean
  hmac_secret?: string
}

export interface CreateAPIPrincipalTestTokenPayload {
  external_user_id: string
  expires_in_seconds?: number
}

export interface APIPrincipalTestToken {
  token: string
  header_name: string
  expires_in_seconds: number
  expires_at_unix: number
  external_user_id: string
}

// Bounded per-key grants for non-full-access API keys.
//  - 'retrieve': read/search knowledge-base data within scope
//  - 'chat': run the conversation flow (sessions + agent listing + self identity)
//  - 'read_agents': list/read agents without chat or authoring
//  - 'ingest': write content into allowed knowledge bases (docs/chunks/FAQ/tags/wiki)
//  - 'manage_kbs': manage existing knowledge-base metadata/configuration
//  - 'manage_agents': create/update/delete/copy agents
//  - 'message_history': search/read tenant chat-history metadata
//  - 'manage_models': manage tenant model definitions, checks, and credentials
//  - 'manage_mcp_services': manage MCP services, credentials, tool policies, and OAuth state
//  - 'manage_datasources': manage data-source connectors and sync jobs
//  - 'manage_channels': manage embed and IM channels
//  - 'manage_vector_stores': manage vector stores and parser/storage checks
//  - 'manage_web_search': manage web-search providers
//  - 'run_evaluations': run/read evaluation jobs
//  - 'manage_members': manage tenant members and invitations
//  - 'manage_spaces': manage organization/space collaboration
//  - 'manage_tenant_settings': read/update tenant integration settings (API principal mode, headers, tenant KV)
export type TenantAPIKeyCapability =
  | 'retrieve'
  | 'chat'
  | 'read_agents'
  | 'ingest'
  | 'manage_kbs'
  | 'manage_agents'
  | 'message_history'
  | 'manage_models'
  | 'manage_mcp_services'
  | 'manage_datasources'
  | 'manage_channels'
  | 'manage_vector_stores'
  | 'manage_web_search'
  | 'run_evaluations'
  | 'manage_members'
  | 'manage_spaces'
  | 'manage_tenant_settings'

export interface TenantAPIKey {
  id: number
  name: string
  api_key: string
  full_access: boolean
  knowledge_base_ids: string[]
  capabilities?: TenantAPIKeyCapability[]
  last_used_at?: string
  expires_at?: string
  created_at: string
}

export interface CreatedTenantAPIKey extends TenantAPIKey {
  token?: string
}

export interface CreateTenantAPIKeyPayload {
  name: string
  full_access?: boolean
  knowledge_base_ids?: string[]
  capabilities?: TenantAPIKeyCapability[]
  expires_at_unix?: number
}

// 搜索租户参数
export interface SearchTenantsParams {
  keyword?: string
  tenant_id?: number
  page?: number
  page_size?: number
}

// 搜索租户响应
export interface SearchTenantsResponse {
  success: boolean
  data?: {
    items: TenantInfo[]
    total: number
    page: number
    page_size: number
  }
  message?: string
}

/**
 * 获取所有租户列表（需要跨租户访问权限）
 * @deprecated 建议使用 searchTenants 代替，支持分页和搜索
 */
export async function listAllTenants(): Promise<{ success: boolean; data?: { items: TenantInfo[] }; message?: string }> {
  try {
    const response = await get('/api/v1/tenants/all')
    return response as unknown as { success: boolean; data?: { items: TenantInfo[] }; message?: string }
  } catch (error: any) {
    return {
      success: false,
      message: error.message || t('error.tenant.listFailed')
    }
  }
}

export async function getAPIPrincipalConfig(
  tenantId: number,
): Promise<{ success: boolean; data?: APIPrincipalConfig; message?: string }> {
  try {
    const response = await get(`/api/v1/tenants/${tenantId}/api-principal-config`)
    return response as unknown as { success: boolean; data?: APIPrincipalConfig; message?: string }
  } catch (error: any) {
    return {
      success: false,
      message: error.message || t('error.tenant.getApiPrincipalConfigFailed'),
    }
  }
}

export async function updateAPIPrincipalConfig(
  tenantId: number,
  payload: UpdateAPIPrincipalConfigPayload,
): Promise<{ success: boolean; data?: APIPrincipalConfig; message?: string }> {
  try {
    const response = await put(`/api/v1/tenants/${tenantId}/api-principal-config`, payload)
    return response as unknown as { success: boolean; data?: APIPrincipalConfig; message?: string }
  } catch (error: any) {
    return {
      success: false,
      message: error.message || t('error.tenant.updateApiPrincipalConfigFailed'),
    }
  }
}

export async function createAPIPrincipalTestToken(
  tenantId: number,
  payload: CreateAPIPrincipalTestTokenPayload,
): Promise<{ success: boolean; data?: APIPrincipalTestToken; message?: string }> {
  try {
    const response = await post(`/api/v1/tenants/${tenantId}/api-principal-test-token`, payload)
    return response as unknown as { success: boolean; data?: APIPrincipalTestToken; message?: string }
  } catch (error: any) {
    return {
      success: false,
      message: error.message || t('error.tenant.createApiPrincipalTestTokenFailed'),
    }
  }
}

export async function listTenantAPIKeys(
  tenantId: number,
): Promise<{ success: boolean; data?: TenantAPIKey[]; message?: string }> {
  try {
    const response = await get(`/api/v1/tenants/${tenantId}/api-keys`)
    return response as unknown as { success: boolean; data?: TenantAPIKey[]; message?: string }
  } catch (error: any) {
    return {
      success: false,
      message: error.message || t('error.tenant.listApiKeysFailed'),
    }
  }
}

export async function createTenantAPIKey(
  tenantId: number,
  payload: CreateTenantAPIKeyPayload,
): Promise<{ success: boolean; data?: CreatedTenantAPIKey; message?: string }> {
  try {
    const response = await post(`/api/v1/tenants/${tenantId}/api-keys`, payload)
    return response as unknown as { success: boolean; data?: CreatedTenantAPIKey; message?: string }
  } catch (error: any) {
    return {
      success: false,
      message: error.message || t('error.tenant.createApiKeyFailed'),
    }
  }
}

export async function deleteTenantAPIKey(
  tenantId: number,
  keyId: number,
): Promise<{ success: boolean; message?: string }> {
  try {
    const response = await del(`/api/v1/tenants/${tenantId}/api-keys/${keyId}`)
    return response as unknown as { success: boolean; message?: string }
  } catch (error: any) {
    return {
      success: false,
      message: error.message || t('error.tenant.deleteApiKeyFailed'),
    }
  }
}

/**
 * 更新租户信息（目前暴露名称、描述两个字段的编辑入口）。
 * 后端 `PUT /tenants/:id` 用指针字段区分"未传"和"显式空串"，未传的列不会
 * 被改动；这里也按需选择性传 `name` / `description`，互不影响。
 * 权限：owner（与 router.go 中的 g.Owner() 守卫保持一致）。
 */
export async function updateTenant(
  tenantId: number,
  payload: { name?: string; description?: string },
): Promise<{ success: boolean; data?: TenantInfo; message?: string }> {
  try {
    const response = await put(`/api/v1/tenants/${tenantId}`, payload)
    return response as unknown as { success: boolean; data?: TenantInfo; message?: string }
  } catch (error: any) {
    return {
      success: false,
      message: error.message || t('error.tenant.updateFailed'),
    }
  }
}

/**
 * 删除当前工作区。权限：owner。
 */
export async function deleteTenant(
  tenantId: number,
): Promise<{ success: boolean; message?: string }> {
  try {
    const response = await del(`/api/v1/tenants/${tenantId}`)
    return response as unknown as { success: boolean; message?: string }
  } catch (error: any) {
    return {
      success: false,
      message: error.message || t('error.tenant.deleteFailed'),
    }
  }
}

/**
 * 创建新工作区（任意已登录用户均可调用）。
 * 后端会自动把调用者写成新租户的 Owner，并填充默认 storage_quota
 * 等服务端字段；API Key 由用户在集成页手动创建。
 * 路由：POST /api/v1/tenants（router 上不挂 g.CrossTenant()，自助场景使用）。
 */
export async function createTenant(
  payload: { name: string; description?: string },
): Promise<{ success: boolean; data?: TenantInfo; message?: string }> {
  try {
    const response = await post('/api/v1/tenants', payload)
    return response as unknown as { success: boolean; data?: TenantInfo; message?: string }
  } catch (error: any) {
    const code = error?.error?.code ?? error?.code
    return {
      success: false,
      message: code === 2005
        ? t('tenant.create.disabled')
        : (error.message || t('error.tenant.createFailed')),
    }
  }
}

/**
 * 搜索租户（支持分页、关键词搜索和租户ID过滤）
 */
export async function searchTenants(params: SearchTenantsParams = {}): Promise<SearchTenantsResponse> {
  try {
    const queryParams = new URLSearchParams()
    if (params.keyword) {
      queryParams.append('keyword', params.keyword)
    }
    if (params.tenant_id) {
      queryParams.append('tenant_id', String(params.tenant_id))
    }
    if (params.page) {
      queryParams.append('page', String(params.page))
    }
    if (params.page_size) {
      queryParams.append('page_size', String(params.page_size))
    }
    
    const queryString = queryParams.toString()
    const url = `/api/v1/tenants/search${queryString ? '?' + queryString : ''}`
    const response = await get(url)
    return response as unknown as SearchTenantsResponse
  } catch (error: any) {
    return {
      success: false,
      message: error.message || t('error.tenant.searchFailed')
    }
  }
}
