import { get } from '@/utils/request'

// Tenant information interface
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

// Search tenants parameters
export interface SearchTenantsParams {
  keyword?: string
  tenant_id?: number
  page?: number
  page_size?: number
}

// Search tenants response
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
 * Get all tenants list (requires cross-tenant access permission)
 * @deprecated Recommend using searchTenants instead, supports pagination and search
 */
export async function listAllTenants(): Promise<{ success: boolean; data?: { items: TenantInfo[] }; message?: string }> {
  try {
    const response = await get('/api/v1/tenants/all')
    return response as unknown as { success: boolean; data?: { items: TenantInfo[] }; message?: string }
  } catch (error: any) {
    return {
      success: false,
      message: error.message || '테넌트 목록 가져오기 실패'
    }
  }
}

/**
 * Search tenants (supports pagination, keyword search, and tenant ID filtering)
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
      message: error.message || '테넌트 검색 실패'
    }
  }
}
