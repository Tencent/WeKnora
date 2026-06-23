import { get, post, put, del } from '@/utils/request'

export interface SystemInfo {
  version: string
  edition?: string
  commit_id?: string
  build_time?: string
  go_version?: string
  keyword_index_engine?: string
  vector_store_engine?: string
  graph_database_engine?: string
  minio_enabled?: boolean
  db_version?: string
  /** Human-readable error message when the startup migration failed.
   *  When non-empty, the system info view should surface a troubleshooting
   *  banner (see docs/migration-troubleshooting.md). */
  db_migration_error?: string
  /** Server process boot time (RFC3339, UTC). */
  started_at?: string
  /** Seconds since process start. */
  uptime_seconds?: number
}

export interface PlaceholderDefinition {
  name: string
  label: string
  description: string
}

export interface PromptTemplate {
  id: string
  name: string
  description: string
  content: string
  user?: string
  has_knowledge_base?: boolean
  has_web_search?: boolean
  default?: boolean
  mode?: string
}

export interface PromptTemplatesConfig {
  system_prompt: PromptTemplate[]
  context_template: PromptTemplate[]
  // Rewrite templates — each template contains both content (system) + user fields
  rewrite: PromptTemplate[]
  // Fallback templates — fixed responses + model fallback prompts (mode: "model")
  fallback: PromptTemplate[]

  generate_session_title?: PromptTemplate[]
  generate_summary?: PromptTemplate[]
  keywords_extraction?: PromptTemplate[]
  chat_summary?: PromptTemplate[]
  agent_system_prompt?: PromptTemplate[]
  intent_prompts?: PromptTemplate[]
}

export function getSystemInfo(): Promise<{ data: SystemInfo }> {
  return get('/api/v1/system/info')
}

export function getPromptTemplates(): Promise<{ data: PromptTemplatesConfig }> {
  return get('/api/v1/tenants/kv/prompt-templates')
}

export interface ParserEngineInfo {
  Name: string
  Description: string
  FileTypes: string[]
  Available?: boolean
  UnavailableReason?: string
}

/** 解析引擎配置（引擎相关存租户；docreader 地址由环境变量配置） */
export interface ParserEngineConfig {
  docreader_addr?: string
  docreader_transport?: string
  mineru_endpoint?: string
  mineru_api_key?: string
  // MinerU 自建参数
  mineru_model?: string
  mineru_vlm_server_url?: string
  mineru_enable_formula?: boolean | null
  mineru_enable_table?: boolean | null
  mineru_enable_ocr?: boolean | null
  mineru_language?: string
  // MinerU 云 API 参数
  mineru_cloud_model?: string
  mineru_cloud_enable_formula?: boolean | null
  mineru_cloud_enable_table?: boolean | null
  mineru_cloud_enable_ocr?: boolean | null
  mineru_cloud_language?: string
  // PaddleOCR-VL 自建参数
  paddleocr_vl_endpoint?: string
  paddleocr_vl_use_seal_recognition?: boolean | null
  paddleocr_vl_use_chart_recognition?: boolean | null
  // PaddleOCR-VL 云 API 参数
  paddleocr_vl_cloud_token?: string
  paddleocr_vl_cloud_model?: string
  paddleocr_vl_cloud_use_seal_recognition?: boolean | null
  paddleocr_vl_cloud_use_chart_recognition?: boolean | null
}

export interface ParserEnginesResponse {
  data: ParserEngineInfo[]
  docreader_addr?: string
  /** 连接方式：grpc | http，由服务端环境/配置决定 */
  docreader_transport?: string
  connected?: boolean
}

export function getParserEngines(): Promise<ParserEnginesResponse> {
  return get('/api/v1/system/parser-engines')
}

/** 使用当前填写的参数检测引擎可用性（不保存），用于填写新参数后即时测试 */
export function checkParserEngines(config: ParserEngineConfig): Promise<ParserEnginesResponse> {
  return post('/api/v1/system/parser-engines/check', config)
}

export function getParserEngineConfig(): Promise<{ data: ParserEngineConfig }> {
  return get('/api/v1/tenants/kv/parser-engine-config')
}

export function updateParserEngineConfig(config: ParserEngineConfig): Promise<{ data: ParserEngineConfig }> {
  return put('/api/v1/tenants/kv/parser-engine-config', config)
}

export function reconnectDocReader(addr: string): Promise<ParserEnginesResponse & { msg?: string }> {
  return post('/api/v1/system/docreader/reconnect', { addr })
}

// ---- 存储引擎配置（租户级，供文档/图片存储与 docreader 使用） ----

export interface StorageEngineConfig {
  default_provider: string // "local" | "minio" | "cos" | "tos" | "s3" | "oss" | "ks3" | "obs"
  local: { path_prefix: string }
  minio: { mode: string; endpoint: string; access_key_id: string; secret_access_key: string; bucket_name: string; use_ssl: boolean; path_prefix: string }
  cos: {
    secret_id: string
    secret_key: string
    region: string
    bucket_name: string
    app_id: string
    path_prefix: string
  }
  tos: {
    endpoint: string
    region: string
    access_key: string
    secret_key: string
    bucket_name: string
    path_prefix: string
  }
  s3: {
    endpoint: string
    region: string
    access_key: string
    secret_key: string
    bucket_name: string
    path_prefix: string
  }
  oss: {
    endpoint: string
    region: string
    access_key: string
    secret_key: string
    bucket_name: string
    path_prefix: string
    use_temp_bucket: boolean
    temp_bucket_name: string
    temp_region: string
  }
  ks3: {
    endpoint: string
    region: string
    access_key: string
    secret_key: string
    bucket_name: string
    path_prefix: string
  }
  obs: {
    endpoint: string
    region: string
    access_key: string
    secret_key: string
    bucket_name: string
    path_prefix: string
  }
}

export interface StorageEngineStatusItem {
  name: string
  allowed?: boolean
  available: boolean
  description: string
}

export interface GetStorageEngineStatusResponse {
  engines: StorageEngineStatusItem[]
  allowed_providers?: string[]
  minio_env_available: boolean
}

export function getStorageEngineConfig(): Promise<{ data: StorageEngineConfig }> {
  return get('/api/v1/tenants/kv/storage-engine-config')
}

export function updateStorageEngineConfig(config: StorageEngineConfig): Promise<{ data: StorageEngineConfig }> {
  return put('/api/v1/tenants/kv/storage-engine-config', config)
}

export function getStorageEngineStatus(): Promise<{ data: GetStorageEngineStatusResponse }> {
  return get('/api/v1/system/storage-engine-status')
}

export interface StorageCheckRequest {
  provider: string // "minio" | "cos" | "tos" | "s3" | "oss" | "ks3" | "obs"
  minio?: StorageEngineConfig['minio']
  cos?: StorageEngineConfig['cos']
  tos?: StorageEngineConfig['tos']
  s3?: StorageEngineConfig['s3']
  oss?: StorageEngineConfig['oss']
  ks3?: StorageEngineConfig['ks3']
  obs?: StorageEngineConfig['obs']
}

export interface StorageCheckResponse {
  ok: boolean
  message: string
  bucket_created?: boolean
}

export function checkStorageEngine(req: StorageCheckRequest): Promise<{ data: StorageCheckResponse }> {
  return post('/api/v1/system/storage-engine-check', req)
}

// ---- System Admin Management ----

export interface SystemAdminUser {
  id: string
  username: string
  email: string
  avatar?: string
  is_active: boolean
  is_system_admin: boolean
  created_at: string
  updated_at: string
}

export interface PromoteUserRequest {
  user_id: string
}

export interface RevokeSystemAdminRequest {
  user_id: string
}

export interface ListSystemAdminsResponse {
  total: number
  admins: SystemAdminUser[]
}

/**
 * Promote a user to system administrator.
 *
 * Identify the target either by user_id (UUID, for API clients) or
 * email (the human-friendly path used by the SystemAdmin UI). Backend
 * accepts whichever is provided; user_id wins when both are set.
 *
 * Backend handler (system.go) returns the updated UserInfo directly as
 * the response body — no {data: ...} wrapping. The shared axios
 * interceptor in utils/request.ts unwraps response.data at the
 * interceptor layer, so the resolved value here IS the UserInfo.
 *
 * The `as unknown as T` cast is the project-wide pattern for telling
 * TS "trust me, the interceptor unwraps this" — see api/auth/index.ts
 * for the same convention. A naked `Promise<T>` annotation would
 * compile (sometimes — vue-tsc is inconsistent on AxiosResponse vs
 * inline interface assignability) but is fragile.
 */
export interface PromoteUserToSystemAdminRequest {
  /** UUID of the user to promote. Optional; supply this OR `email`. */
  user_id?: string
  /** Email address of the user to promote. Optional; supply this OR `user_id`. */
  email?: string
}

export async function promoteUserToSystemAdmin(
  req: PromoteUserToSystemAdminRequest,
): Promise<SystemAdminUser> {
  const response = await post('/api/v1/system/admin/promote', req)
  return response as unknown as SystemAdminUser
}

/**
 * Revoke system administrator privileges from a user.
 * Same wrapping convention as promoteUserToSystemAdmin.
 */
export async function revokeSystemAdmin(userId: string): Promise<SystemAdminUser> {
  const response = await post('/api/v1/system/admin/revoke', { user_id: userId })
  return response as unknown as SystemAdminUser
}

/**
 * List all system administrators (paginated).
 * Returns {total, admins[]} directly — no {data: ...} wrapping.
 */
export async function listSystemAdmins(
  params?: { offset?: number; limit?: number },
): Promise<ListSystemAdminsResponse> {
  // The shared `get` helper doesn't accept a config object, so we
  // assemble the query string manually. Both params are optional;
  // the server applies sane defaults (offset=0, limit=50, max=200).
  const qs = new URLSearchParams()
  if (params?.offset != null) qs.set('offset', String(params.offset))
  if (params?.limit != null) qs.set('limit', String(params.limit))
  const suffix = qs.toString() ? `?${qs.toString()}` : ''
  const response = await get(`/api/v1/system/admin/list${suffix}`)
  return response as unknown as ListSystemAdminsResponse
}

// ---- System Settings (P1) ----

/**
 * SystemSettingItem mirrors types.SystemSetting on the backend, exactly
 * as the JSON API serialises it (no `data: ...` wrapping; see
 * utils/request.ts:97 — the axios interceptor unwraps response.data
 * project-wide). New fields here MUST also be added to backend
 * types/system_setting.go.
 *
 * `value` is typed as `unknown` because the underlying JSONB column can
 * hold an int / string / bool depending on `value_type`. Callers narrow
 * via the value_type field (`'int' | 'string' | 'bool'`).
 */
export interface SystemSettingItem {
  id: number
  key: string
  /** Raw JSON value — narrow via value_type before rendering. */
  value: unknown
  value_type: 'int' | 'string' | 'bool' | 'string_list'
  category: string
  description: string
  /** P3+ — currently always false. UI may surface a "redacted" state when true. */
  is_secret: boolean
  /** P3+ — currently always false. UI may show "needs restart to take effect" badge when true. */
  requires_restart: boolean
  last_modified_by: string
  /**
   * Display label resolved from last_modified_by (UUID) on the server —
   * username when known, email as a fallback. Empty/undefined for
   * virtual rows that were never persisted; UI then falls back to the
   * UUID prefix.
   */
   last_modified_by_name?: string
  created_at: string
  updated_at: string
  /**
   * Allowed values for `value` when this setting is constrained. Populated by
   * the service from the in-code registry; absent/empty means "free-form".
   * Frontend renders a t-select instead of t-input when this is non-empty.
   */
  enum?: string[]
}

/**
 * List every system setting row (system-scope, not tenant-scope).
 * Backend returns the array directly; we cast through `unknown` to match
 * the project-wide axios contract (see utils/request.ts:97).
 */
export async function listSystemSettings(): Promise<SystemSettingItem[]> {
  const response = await get('/api/v1/system/admin/settings')
  return response as unknown as SystemSettingItem[]
}

/**
 * Fetch a single system setting by key. Throws (via the axios interceptor)
 * if the key is unknown to the registry, or if the row is not yet persisted.
 */
export async function getSystemSetting(key: string): Promise<SystemSettingItem> {
  const response = await get(`/api/v1/system/admin/settings/${encodeURIComponent(key)}`)
  return response as unknown as SystemSettingItem
}

/**
 * Persist a new value for `key`. The backend validates the value against
 * the registry-declared value_type and rejects mismatches with 400; the
 * error message is surfaced via err.message (see utils/request.ts:209).
 *
 * Successful updates emit an audit row (action=system.setting_changed)
 * carrying old/new values for forensics.
 */
export async function updateSystemSetting(
  key: string,
  value: unknown,
): Promise<SystemSettingItem> {
  const response = await put(
    `/api/v1/system/admin/settings/${encodeURIComponent(key)}`,
    { value },
  )
  return response as unknown as SystemSettingItem
}

/**
 * Reset a system setting back to its ENV / built-in default by deleting
 * the DB override row. Idempotent — resetting a key that was never
 * persisted resolves successfully.
 */
export async function resetSystemSetting(key: string): Promise<void> {
  await del(`/api/v1/system/admin/settings/${encodeURIComponent(key)}`)
}

/**
 * Result of POST /system/admin/tenants/apply-default-storage-quota.
 * `affected` is the count of tenant rows whose storage_quota was
 * overwritten; `quota_bytes` is the value written.
 */
export interface ApplyDefaultStorageQuotaResult {
  affected: number
  quota_bytes: number
  quota_gb: number
}

/**
 * Apply the current `tenant.default_storage_quota_gb` setting to every
 * existing tenant. Reads the resolved setting server-side (DB > ENV >
 * default), then writes that quota to every row. SystemAdmin only.
 *
 * Idempotent: running twice with the same setting has the same effect.
 */
export async function applyDefaultStorageQuotaToAllTenants(): Promise<ApplyDefaultStorageQuotaResult> {
  const response = await post('/api/v1/system/admin/tenants/apply-default-storage-quota')
  return response as unknown as ApplyDefaultStorageQuotaResult
}

// ---- Platform Audit Log (system-scope) ----

// We reuse the AuditLog / ListAuditLogParams types from the tenant
// audit-log module — the row shape is identical, only the route
// differs (tenant_id=0 rows aren't visible via the per-tenant endpoint).
// Re-exported here so SystemSettings.vue doesn't need to cross-import
// from a tenant-specific module to consume system-scope feeds.
export type {
  AuditLog,
  AuditAction,
  AuditOutcome,
  ListAuditLogParams,
  ListAuditLogResponse,
} from '@/api/tenant/audit-log'

import type { ListAuditLogParams, ListAuditLogResponse } from '@/api/tenant/audit-log'

/**
 * List the platform-wide audit log (system-scope, tenant_id=0).
 *
 * Backend: GET /api/v1/system/admin/audit-log (SystemAdmin only).
 * Covers system.setting_changed / system.admin_promoted /
 * system.admin_revoked etc. — events emitted by SystemAdmin actions.
 *
 * Cursor-paginated by descending id: the first call should pass no
 * cursor, each subsequent page should pass `after_id =
 * previousResponse.next_cursor` until next_cursor comes back as 0.
 */
export async function listSystemAuditLog(
  params: ListAuditLogParams = {},
): Promise<ListAuditLogResponse> {
  const qs = new URLSearchParams()
  if (params.after_id) qs.append('after_id', String(params.after_id))
  if (params.limit) qs.append('limit', String(params.limit))
  if (params.action) qs.append('action', params.action)
  if (params.outcome) qs.append('outcome', params.outcome)
  if (params.actor) qs.append('actor', params.actor)
  const tail = qs.toString()
  const url = `/api/v1/system/admin/audit-log${tail ? '?' + tail : ''}`
  return (await get(url)) as unknown as ListAuditLogResponse
}
