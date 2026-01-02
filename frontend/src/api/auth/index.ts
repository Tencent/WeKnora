import { post, get, put } from '@/utils/request'

// User login interface
export interface LoginRequest {
  email: string
  password: string
}

export interface LoginResponse {
  success: boolean
  message?: string
  user?: {
    id: string
    username: string
    email: string
    avatar?: string
    tenant_id: number
    can_access_all_tenants?: boolean
    is_active: boolean
    created_at: string
    updated_at: string
  }
  tenant?: {
    id: number
    name: string
    description: string
    api_key: string
    status: string
    business: string
    storage_quota: number
    storage_used: number
    created_at: string
    updated_at: string
  }
  token?: string
  refresh_token?: string
}

// User registration interface
export interface RegisterRequest {
  username: string
  email: string
  password: string
}

export interface RegisterResponse {
  success: boolean
  message?: string
  data?: {
    user: {
      id: string
      username: string
      email: string
    }
    tenant: {
      id: string
      name: string
      api_key: string
    }
  }
}

// User information interface
export interface UserInfo {
  id: string
  username: string
  email: string
  avatar?: string
  tenant_id: string
  can_access_all_tenants?: boolean
  created_at: string
  updated_at: string
}

// Tenant information interface
export interface TenantInfo {
  id: string
  name: string
  description?: string
  api_key: string
  status?: string
  business?: string
  owner_id: string
  storage_quota?: number
  storage_used?: number
  created_at: string
  updated_at: string
  knowledge_bases?: KnowledgeBaseInfo[]
}

// Knowledge base information interface
export interface KnowledgeBaseInfo {
  id: string
  name: string
  description: string
  tenant_id: string
  created_at: string
  updated_at: string
  document_count?: number
  chunk_count?: number
}

// Model information interface
export interface ModelInfo {
  id: string
  name: string
  type: string
  source: string
  description?: string
  is_default?: boolean
  created_at: string
  updated_at: string
}

/**
 * User login
 */
export async function login(data: LoginRequest): Promise<LoginResponse> {
  try {
    const response = await post('/api/v1/auth/login', data)
    return response as unknown as LoginResponse
  } catch (error: any) {
    return {
      success: false,
      message: error.message || '로그인 실패'
    }
  }
}

/**
 * User registration
 */
export async function register(data: RegisterRequest): Promise<RegisterResponse> {
  try {
    const response = await post('/api/v1/auth/register', data)
    return response as unknown as RegisterResponse
  } catch (error: any) {
    return {
      success: false,
      message: error.message || '회원가입 실패'
    }
  }
}

/**
 * Get current user information
 */
export async function getCurrentUser(): Promise<{ success: boolean; data?: { user: UserInfo; tenant: TenantInfo }; message?: string }> {
  try {
    const response = await get('/api/v1/auth/me')
    return response as unknown as { success: boolean; data?: { user: UserInfo; tenant: TenantInfo }; message?: string }
  } catch (error: any) {
    return {
      success: false,
      message: error.message || '사용자 정보 가져오기 실패'
    }
  }
}

/**
 * Get current tenant information
 */
export async function getCurrentTenant(): Promise<{ success: boolean; data?: TenantInfo; message?: string }> {
  try {
    const response = await get('/api/v1/auth/tenant')
    return response as unknown as { success: boolean; data?: TenantInfo; message?: string }
  } catch (error: any) {
    return {
      success: false,
      message: error.message || '테넌트 정보 가져오기 실패'
    }
  }
}

/**
 * Refresh Token
 */
export async function refreshToken(refreshToken: string): Promise<{ success: boolean; data?: { token: string; refreshToken: string }; message?: string }> {
  try {
    const response: any = await post('/api/v1/auth/refresh', { refreshToken })
    if (response && response.success) {
      if (response.access_token || response.refresh_token) {
        return {
          success: true,
          data: {
            token: response.access_token,
            refreshToken: response.refresh_token,
          }
        }
      }
    }

    // Other cases return original message directly
    return {
      success: false,
      message: response?.message || '토큰 갱신 실패'
    }
  } catch (error: any) {
    return {
      success: false,
      message: error.message || '토큰 갱신 실패'
    }
  }
}

/**
 * User logout
 */
export async function logout(): Promise<{ success: boolean; message?: string }> {
  try {
    await post('/api/v1/auth/logout', {})
    return {
      success: true
    }
  } catch (error: any) {
    return {
      success: false,
      message: error.message || '로그아웃 실패'
    }
  }
}

/**
 * Validate Token
 */
export async function validateToken(): Promise<{ success: boolean; valid?: boolean; message?: string }> {
  try {
    const response = await get('/api/v1/auth/validate')
    return response as unknown as { success: boolean; valid?: boolean; message?: string }
  } catch (error: any) {
    return {
      success: false,
      valid: false,
      message: error.message || '토큰 검증 실패'
    }
  }
}
