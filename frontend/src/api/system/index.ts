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
}

export interface ToolDefinition {
  name: string
  label: string
  description: string
}

export interface PlaceholderDefinition {
  name: string
  label: string
  description: string
}

export interface AgentConfig {
  max_iterations: number
  reflection_enabled: boolean
  allowed_tools: string[]
  temperature: number
  knowledge_bases?: string[]
  system_prompt?: string  // Unified system prompt (uses {{web_search_status}} placeholder)
  available_tools?: ToolDefinition[]  // GET 响应中包含，POST/PUT 不需要
  available_placeholders?: PlaceholderDefinition[]  // GET 响应中包含，POST/PUT 不需要
}

export interface ConversationConfig {
  prompt: string
  context_template: string
  temperature: number
  max_completion_tokens: number
  max_rounds: number
  embedding_top_k: number
  keyword_threshold: number
  vector_threshold: number
  rerank_top_k: number
  rerank_threshold: number
  enable_rewrite: boolean
  fallback_strategy: string
  fallback_response: string
  fallback_prompt?: string
  summary_model_id?: string
  rerank_model_id?: string
  rewrite_prompt_system?: string
  rewrite_prompt_user?: string
  enable_query_expansion?: boolean
}

export interface PromptTemplate {
  id: string
  name: string
  description: string
  content: string
  has_knowledge_base?: boolean
  has_web_search?: boolean
}

export interface PromptTemplatesConfig {
  system_prompt: PromptTemplate[]
  context_template: PromptTemplate[]
  rewrite_system: PromptTemplate[]
  rewrite_user: PromptTemplate[]
  fallback: PromptTemplate[]
}

export function getSystemInfo(): Promise<{ data: SystemInfo }> {
  return get('/api/v1/system/info')
}

export function getAgentConfig(): Promise<{ data: AgentConfig }> {
  return get('/api/v1/tenants/kv/agent-config')
}

export function updateAgentConfig(config: AgentConfig): Promise<{ data: AgentConfig }> {
  return put('/api/v1/tenants/kv/agent-config', config)
}

export function getConversationConfig(): Promise<{ data: ConversationConfig }> {
  return get('/api/v1/tenants/kv/conversation-config')
}

export function updateConversationConfig(config: ConversationConfig): Promise<{ data: ConversationConfig }> {
  return put('/api/v1/tenants/kv/conversation-config', config)
}

export function getPromptTemplates(): Promise<{ data: PromptTemplatesConfig }> {
  return get('/api/v1/tenants/kv/prompt-templates')
}

export interface MinioBucketInfo {
  name: string
  policy: 'public' | 'private' | 'custom'
  created_at?: string
}

export interface ListMinioBucketsResponse {
  buckets: MinioBucketInfo[]
}

export function listMinioBuckets(): Promise<{ data: ListMinioBucketsResponse }> {
  return get('/api/v1/system/minio/buckets')
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
  mineru_api_base_url?: string
  // MinerU 自建参数
  mineru_model?: string
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
  default_provider: string // "local" | "minio" | "cos" | "tos"
  local?: { path_prefix: string }
  minio?: { mode: string; endpoint: string; access_key_id: string; secret_access_key: string; bucket_name: string; use_ssl: boolean; path_prefix: string }
  cos?: {
    secret_id: string
    secret_key: string
    region: string
    bucket_name: string
    app_id: string
    path_prefix: string
  }
  tos?: {
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
  available: boolean
  description: string
}

export interface GetStorageEngineStatusResponse {
  engines: StorageEngineStatusItem[]
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
  provider: string // "minio" | "cos" | "tos"
  minio?: StorageEngineConfig['minio']
  cos?: StorageEngineConfig['cos']
  tos?: StorageEngineConfig['tos']
}

export interface StorageCheckResponse {
  ok: boolean
  message: string
}

export function checkStorageEngine(req: StorageCheckRequest): Promise<{ data: StorageCheckResponse }> {
  return post('/api/v1/system/storage-engine-check', req)
}
