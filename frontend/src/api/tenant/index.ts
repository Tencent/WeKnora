import { get, post, put } from '@/utils/request'
import i18n from '@/i18n'

const t = (key: string) => i18n.global.t(key)

// 租户信息接口
export interface TenantInfo {
  id: number
  name: string
  description?: string
  api_key?: string
  status?: string
  business?: string
  storage_quota?: number
  storage_used?: number
  created_at: string
  updated_at: string
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

/**
 * 重置租户的 API Key。成功后返回新的明文 Key，旧 Key 立即失效。
 */
export async function resetTenantApiKey(
  tenantId: number,
): Promise<{ success: boolean; data?: { api_key: string }; message?: string }> {
  try {
    const response = await post(`/api/v1/tenants/${tenantId}/api-key`)
    return response as unknown as { success: boolean; data?: { api_key: string }; message?: string }
  } catch (error: any) {
    return {
      success: false,
      message: error.message || t('error.tenant.resetApiKeyFailed'),
    }
  }
}

/**
 * 更新租户信息（目前仅暴露租户名称编辑入口）。
 * 后端 `PUT /tenants/:id` 走 GORM 的 `Updates(tenant)`，只会写入请求体里
 * 出现的非零字段；这里只发送 `name` 就只会改名字，不会把其它字段清空。
 * 权限：owner（与 router.go 中的 g.Owner() 守卫保持一致）。
 */
export async function updateTenant(
  tenantId: number,
  payload: { name: string },
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
 * 创建新工作区（任意已登录用户均可调用）。
 * 后端会自动把调用者写成新租户的 Owner，并生成 api_key、默认 storage_quota
 * 等服务端字段，所以这里只暴露 name + description。
 * 路由：POST /api/v1/tenants（router 上不挂 g.CrossTenant()，自助场景使用）。
 */
export async function createTenant(
  payload: { name: string; description?: string },
): Promise<{ success: boolean; data?: TenantInfo; message?: string }> {
  try {
    const response = await post('/api/v1/tenants', payload)
    return response as unknown as { success: boolean; data?: TenantInfo; message?: string }
  } catch (error: any) {
    return {
      success: false,
      message: error.message || t('error.tenant.createFailed'),
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

