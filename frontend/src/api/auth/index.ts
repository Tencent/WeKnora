import { post, get, put } from '@/utils/request'
import i18n from '@/i18n'

const t = (key: string) => i18n.global.t(key)

// 用户登录接口
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

export interface OIDCAuthURLResponse {
  success: boolean
  authorization_url?: string
  state?: string
  message?: string
}

export interface OIDCConfigResponse {
  success: boolean
  enabled: boolean
  provider_display_name?: string
  message?: string
}

// 用户注册接口
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

// 用户信息接口
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

// 租户信息接口
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

// 知识库信息接口
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

// 模型信息接口
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
 * 用户登录
 */
export async function login(data: LoginRequest): Promise<LoginResponse> {
  try {
    const response = await post('/api/v1/auth/login', data)
    return response as unknown as LoginResponse
  } catch (error: any) {
    return {
      success: false,
      message: error.message || t('error.auth.loginFailed')
    }
  }
}

/**
 * 获取 OIDC 登录跳转地址
 */
export async function getOIDCAuthorizationURL(redirectURI: string): Promise<OIDCAuthURLResponse> {
  try {
    const response = await get(`/api/v1/auth/oidc/url?redirect_uri=${encodeURIComponent(redirectURI)}`)
    return response as unknown as OIDCAuthURLResponse
  } catch (error: any) {
    return {
      success: false,
      message: error.message || t('error.auth.loginFailed')
    }
  }
}

/**
 * 获取 OIDC 登录配置
 */
export async function getOIDCConfig(): Promise<OIDCConfigResponse> {
  try {
    const response = await get('/api/v1/auth/oidc/config')
    return response as unknown as OIDCConfigResponse
  } catch (error: any) {
    return {
      success: false,
      enabled: false,
      message: error.message || t('error.auth.loginFailed')
    }
  }
}

/**
 * 用户注册
 */
export async function register(data: RegisterRequest): Promise<RegisterResponse> {
  try {
    const response = await post('/api/v1/auth/register', data)
    return response as unknown as RegisterResponse
  } catch (error: any) {
    return {
      success: false,
      message: error.message || t('error.auth.registerFailed')
    }
  }
}

/**
 * 获取当前用户信息
 */
export async function getCurrentUser(): Promise<{ success: boolean; data?: { user: UserInfo; tenant: TenantInfo }; message?: string }> {
  try {
    const response = await get('/api/v1/auth/me')
    return response as unknown as { success: boolean; data?: { user: UserInfo; tenant: TenantInfo }; message?: string }
  } catch (error: any) {
    return {
      success: false,
      message: error.message || t('error.auth.getUserFailed')
    }
  }
}

/**
 * 获取当前租户信息
 */
export async function getCurrentTenant(): Promise<{ success: boolean; data?: TenantInfo; message?: string }> {
  try {
    const response = await get('/api/v1/auth/tenant')
    return response as unknown as { success: boolean; data?: TenantInfo; message?: string }
  } catch (error: any) {
    return {
      success: false,
      message: error.message || t('error.auth.getTenantFailed')
    }
  }
}

/**
 * 刷新Token
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

    // 其他情况直接返回原始消息
    return {
      success: false,
      message: response?.message || t('error.auth.refreshTokenFailed')
    }
  } catch (error: any) {
    return {
      success: false,
      message: error.message || t('error.auth.refreshTokenFailed')
    }
  }
}

/**
 * 用户登出
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
      message: error.message || t('error.auth.logoutFailed')
    }
  }
}

/**
 * 验证Token有效性
 */
export async function validateToken(): Promise<{ success: boolean; valid?: boolean; message?: string }> {
  try {
    const response = await get('/api/v1/auth/validate')
    return response as unknown as { success: boolean; valid?: boolean; message?: string }
  } catch (error: any) {
    return {
      success: false,
      valid: false,
      message: error.message || t('error.auth.validateTokenFailed')
    }
  }
}




