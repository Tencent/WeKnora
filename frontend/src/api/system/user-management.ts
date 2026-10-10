/**
 * API client for Settings → 系统管理 → 用户管理.
 *
 * Three tabs share one module because they are one screen and one backend
 * feature area (internal/handler/user_management.go):
 *
 *   - 本地用户    list / disable / enable / delete + the existing
 *                 create-user and reset-password dialogs (api/system/index.ts)
 *   - 通用OIDC    DB-backed OIDC provider, superseding the OIDC_AUTH_* env vars
 *   - LDAP        directory binding used as a login fallback
 *
 * Response shapes mirror internal/types/auth_provider.go exactly. The shared
 * axios interceptor in utils/request.ts unwraps `response.data`, so the
 * resolved value IS the payload — hence the `as unknown as T` casts, the
 * project-wide convention for this (see api/system/index.ts).
 */
import { get, post, put, del } from '@/utils/request'

// ---------------------------------------------------------------------------
// Shared enums
// ---------------------------------------------------------------------------

/** Where the effective provider configuration came from. */
export type ProviderConfigSource = 'database' | 'environment' | 'default'

/** How a local account authenticates. */
export type AccountAuthSource = 'local' | 'oidc' | 'ldap'

// ---------------------------------------------------------------------------
// 本地用户
// ---------------------------------------------------------------------------

export interface ManagedUser {
  id: string
  username: string
  email: string
  avatar: string
  /** 0 when the account has no workspace (e.g. a tombstoned row). */
  tenant_id: number
  tenant_name?: string
  is_active: boolean
  is_system_admin: boolean
  can_access_all_tenants: boolean
  auth_source: AccountAuthSource
  /** False for OIDC-only accounts and for rows whose password hash is empty. */
  has_local_password: boolean
  created_at: string
  updated_at: string
}

export interface ManagedUserStats {
  total: number
  active: number
  disabled: number
  admins: number
  ldap_users: number
  oidc_users: number
}

export interface ListManagedUsersResponse {
  total: number
  users: ManagedUser[]
  /** Counters cover the whole table, not just the returned page. */
  stats: ManagedUserStats
}

export interface ListManagedUsersParams {
  keyword?: string
  offset?: number
  limit?: number
}

/**
 * List local user accounts (SystemAdmin only).
 *
 * Paginated; `keyword` is a case-insensitive substring match over username and
 * email. The shared `get` helper takes no config object, so the query string is
 * assembled by hand.
 */
export async function listManagedUsers(
  params: ListManagedUsersParams = {},
): Promise<ListManagedUsersResponse> {
  const qs = new URLSearchParams()
  if (params.keyword) qs.set('keyword', params.keyword)
  if (params.offset != null) qs.set('offset', String(params.offset))
  if (params.limit != null) qs.set('limit', String(params.limit))
  const suffix = qs.toString() ? `?${qs.toString()}` : ''
  const response = await get(`/api/v1/system/admin/users${suffix}`)
  return response as unknown as ListManagedUsersResponse
}

/** Disable an account. The server refuses to disable the calling admin. */
export async function disableManagedUser(userId: string): Promise<{ success: boolean }> {
  const response = await post(`/api/v1/system/admin/users/${userId}/disable`, {})
  return response as unknown as { success: boolean }
}

/** Re-enable a previously disabled account. */
export async function enableManagedUser(userId: string): Promise<{ success: boolean }> {
  const response = await post(`/api/v1/system/admin/users/${userId}/enable`, {})
  return response as unknown as { success: boolean }
}

/**
 * Delete an account (soft delete + tombstone).
 *
 * The server refuses to delete the caller and refuses to remove the last
 * remaining system administrator.
 */
export async function deleteManagedUser(userId: string): Promise<{ success: boolean }> {
  const response = await del(`/api/v1/system/admin/users/${userId}`)
  return response as unknown as { success: boolean }
}

// ---------------------------------------------------------------------------
// 通用OIDC
// ---------------------------------------------------------------------------

export interface OIDCProviderView {
  success: boolean
  enabled: boolean
  issuer_url: string
  discovery_url: string
  provider_display_name: string
  client_id: string
  /** The client secret is write-only; the server reports only its presence. */
  has_secret: boolean
  authorization_endpoint: string
  token_endpoint: string
  user_info_endpoint: string
  jwks_uri: string
  scopes: string[]
  username_claim: string
  email_claim: string
  source: ProviderConfigSource
  updated_at?: string
  updated_by?: string
  updated_by_name?: string
  /**
   * Legacy OIDC_AUTH_* variables still set on this deployment. They act as
   * defaults only until a row is saved; once saved, the DB row wins for every
   * field and these are reported for visibility alone.
   */
  env_overrides?: Record<string, string>
}

export interface OIDCProviderUpdateRequest {
  enabled: boolean
  issuer_url: string
  discovery_url: string
  provider_display_name: string
  client_id: string
  /**
   * Write-only. Omit (or send '') to keep the stored secret. Set
   * `clear_secret` to wipe it deliberately.
   */
  client_secret?: string
  authorization_endpoint: string
  token_endpoint: string
  user_info_endpoint: string
  jwks_uri: string
  scopes: string[]
  username_claim: string
  email_claim: string
  clear_secret?: boolean
}

export interface OIDCTestResponse {
  success: boolean
  message: string
  discovery_ok: boolean
  issuer?: string
  authorization_endpoint?: string
  token_endpoint?: string
  user_info_endpoint?: string
  jwks_uri?: string
  token_endpoint_auth_methods?: string[]
  warnings?: string[]
}

export async function getOIDCProvider(): Promise<OIDCProviderView> {
  const response = await get('/api/v1/system/admin/auth/oidc')
  return response as unknown as OIDCProviderView
}

export async function updateOIDCProvider(
  req: OIDCProviderUpdateRequest,
): Promise<OIDCProviderView> {
  const response = await put('/api/v1/system/admin/auth/oidc', req)
  return response as unknown as OIDCProviderView
}

/** Probe the discovery document without persisting anything. */
export async function testOIDCProvider(): Promise<OIDCTestResponse> {
  const response = await post('/api/v1/system/admin/auth/oidc/test', {})
  return response as unknown as OIDCTestResponse
}

// ---------------------------------------------------------------------------
// LDAP
// ---------------------------------------------------------------------------

export interface LDAPProviderView {
  success: boolean
  enabled: boolean
  host: string
  port: number
  use_tls: boolean
  start_tls: boolean
  skip_tls_verify: boolean
  bind_dn: string
  has_bind_password: boolean
  base_dn: string
  user_search_base: string
  user_filter: string
  user_name_attr: string
  user_email_attr: string
  user_display_attr: string
  auto_create_user: boolean
  /** Empty means "follow auth.default_tenant_mode". */
  default_tenant_mode: string
  timeout_seconds: number
  source: ProviderConfigSource
  updated_at?: string
  updated_by?: string
  updated_by_name?: string
}

export interface LDAPProviderUpdateRequest {
  enabled: boolean
  host: string
  port: number
  use_tls: boolean
  start_tls: boolean
  skip_tls_verify: boolean
  bind_dn: string
  /** Write-only; omit or send '' to keep the stored password. */
  bind_password?: string
  base_dn: string
  user_search_base: string
  user_filter: string
  user_name_attr: string
  user_email_attr: string
  user_display_attr: string
  auto_create_user: boolean
  default_tenant_mode: string
  timeout_seconds: number
  clear_bind_password?: boolean
}

export interface LDAPTestResponse {
  success: boolean
  message: string
  service_ok: boolean
  user_found: boolean
  user_dn?: string
  /**
   * The attribute that actually carries the stable subject id for the matched
   * entry (entryUUID on OpenLDAP, objectGUID on AD), so operators do not have
   * to guess.
   */
  detected_subject_attribute?: string
  attributes?: Record<string, string>
  warnings?: string[]
}

export async function getLDAPProvider(): Promise<LDAPProviderView> {
  const response = await get('/api/v1/system/admin/auth/ldap')
  return response as unknown as LDAPProviderView
}

export async function updateLDAPProvider(
  req: LDAPProviderUpdateRequest,
): Promise<LDAPProviderView> {
  const response = await put('/api/v1/system/admin/auth/ldap', req)
  return response as unknown as LDAPProviderView
}

/**
 * Probe the directory. With no credentials this only checks connectivity and
 * the service-account bind; supplying them additionally exercises the user
 * search and a real bind.
 */
export async function testLDAPProvider(req: {
  username?: string
  password?: string
}): Promise<LDAPTestResponse> {
  const response = await post('/api/v1/system/admin/auth/ldap/test', req)
  return response as unknown as LDAPTestResponse
}
