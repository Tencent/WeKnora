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
  // creator_id is the user id of whoever originally created the KB.
  // Set by PR 5 of the multi-tenant RBAC series; nullable for legacy
  // KBs created before that migration backfilled the column.
  creator_id?: string
  // creator_name 由后端 list 接口批量回填（username 优先，退化到 email），
  // 仅用于列表卡片来源徽章；缺失代表无法解析（已删除 / 老数据）。
  creator_name?: string
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
 * 获取认证配置（仅返回前端渲染需要的公开字段，例如注册模式）。
 *
 * 后端通过 `auth.registration_mode` 控制是否允许自助注册：
 *   - "self_serve"  保留现有自助注册入口（默认）
 *   - "invite_only" 关闭注册，要求管理员邀请
 *
 * 失败时回落到 self_serve，避免接口异常导致注册入口直接消失。
 */
export interface AuthConfigResponse {
  success: boolean
  registration_mode: 'self_serve' | 'invite_only' | string
}

export async function getAuthConfig(): Promise<AuthConfigResponse> {
  try {
    const response = await get('/api/v1/auth/config')
    return response as unknown as AuthConfigResponse
  } catch {
    return { success: false, registration_mode: 'self_serve' }
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
 * Lite 版自动初始化（创建默认用户/租户 + 签发令牌）
 */
export async function autoSetup(): Promise<LoginResponse> {
  try {
    const response = await post('/api/v1/auth/auto-setup', {})
    return response as unknown as LoginResponse
  } catch (error: any) {
    return {
      success: false,
      message: error.message || 'Auto-setup unavailable'
    }
  }
}

/**
 * Membership row returned alongside /auth/me. Mirrors the LoginResponse
 * shape so the frontend can refresh `currentTenantRole` on every page
 * load — without it, role changes after login (e.g. an Owner demoting
 * us in a peer tenant) stay invisible until the user logs out and back
 * in.
 */
export interface MembershipInfo {
  tenant_id: number
  tenant_name?: string
  role: string
}

/**
 * 获取当前用户信息
 */
export async function getCurrentUser(): Promise<{ success: boolean; data?: { user: UserInfo; tenant?: TenantInfo | null; memberships?: MembershipInfo[] }; message?: string }> {
  try {
    const response = await get('/api/v1/auth/me')
    return response as unknown as { success: boolean; data?: { user: UserInfo; tenant?: TenantInfo | null; memberships?: MembershipInfo[] }; message?: string }
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




