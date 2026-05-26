import { get, post, put } from '@/utils/request'

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
