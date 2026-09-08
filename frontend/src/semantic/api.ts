// 数据建模模块 API 客户端 (后端 /api/v1/semantic/*)。
import { get, post, put, del } from '@/utils/request'

// ---- 类型 ----

export interface ConnectionInfo {
  id: string
  name: string
  title: string
  description: string
  type: string
  guided: boolean
  status: string
  /** 非敏感连接参数 (编辑回显用); 密码永不下发 */
  host?: string
  port?: number
  database?: string
  username?: string
  extra?: Record<string, unknown>
  last_test_result?: { ok?: boolean; latency_ms?: number; error?: string } | null
  has_password: boolean
  created_by: string
  created_at: string
  updated_at: string
}

export interface ConnectionInput {
  name?: string
  title?: string
  description?: string
  type?: string
  host?: string
  port?: number
  database?: string
  username?: string
  password?: string
  extra?: Record<string, unknown>
}

export interface TableRef {
  schema: string
  name: string
}

export interface ColumnSchema {
  name: string
  data_type: string
  primary_key: boolean
  nullable: boolean
}

export type ModelStatus = 'draft' | 'published' | 'publish_failed'

export interface SemanticModel {
  id: string
  tenant_id: number
  name: string
  title: string
  description: string
  connection_id: string
  kind: string
  draft_yaml: string
  published_yaml?: string
  status: ModelStatus
  last_error: string
  allowed_groups: string[]
  version: number
  published_at?: string | null
  created_by: string
  created_at: string
  updated_at: string
}

export interface ModelVersion {
  id: string
  model_id: string
  version: number
  note: string
  published_by: string
  published_at: string
}

export interface DataGroup {
  id: string
  name: string
  title: string
  description: string
  created_at?: string
  updated_at?: string
}

export interface ModuleInfo {
  enabled: boolean
  cube_ready: boolean
  cube_error?: string
  guided_types: string[]
}

export interface QueryFilter {
  member?: string
  operator?: string
  values?: string[]
  and?: QueryFilter[]
  or?: QueryFilter[]
}

export interface PreviewQuery {
  measures?: string[]
  dimensions?: string[]
  time_dimensions?: { dimension: string; date_range?: string[]; granularity?: string }[]
  filters?: QueryFilter[]
  order?: Record<string, string>
  limit?: number
  offset?: number
}

export interface PreviewResult {
  data: Record<string, unknown>[]
  hint?: string
}

export const CUBE_TYPES_GUIDED = ['mysql', 'postgres', 'clickhouse', 'sqlserver']

export const CUBE_TYPES_PASSTHROUGH = [
  'clickhouse',
  'mssql',
  'oracle',
  'trino',
  'presto',
  'snowflake',
  'bigquery',
  'redshift',
  'databricks',
  'duckdb',
  'elasticsearch',
  'mongodb',
  'questdb',
  'firebolt',
  'dremio'
]

// ---- 模块信息 ----

export function getModuleInfo() {
  return get<ModuleInfo>('/api/v1/semantic/info')
}

// ---- 连接 ----

export function listConnections() {
  return get<{ connections: ConnectionInfo[] }>('/api/v1/semantic/connections')
}

export function getConnection(id: string) {
  return get<ConnectionInfo>(`/api/v1/semantic/connections/${id}`)
}

export function createConnection(data: ConnectionInput) {
  return post<ConnectionInfo>('/api/v1/semantic/connections', data)
}

export function updateConnection(id: string, data: ConnectionInput) {
  return put<ConnectionInfo>(`/api/v1/semantic/connections/${id}`, data)
}

export function deleteConnection(id: string) {
  return del(`/api/v1/semantic/connections/${id}`)
}

export function testConnectionRaw(data: ConnectionInput) {
  return post<{ test: { ok: boolean; latency_ms?: number; error?: string } }>(
    '/api/v1/semantic/connections/test',
    data
  )
}

export function testConnectionDraft(id: string, data: ConnectionInput) {
  return post<{ test: { ok: boolean; latency_ms?: number; error?: string } }>(
    `/api/v1/semantic/connections/${id}/test-draft`,
    data
  )
}

export function testConnectionSaved(id: string) {
  return post<{ test: { ok: boolean; latency_ms?: number; error?: string } }>(
    `/api/v1/semantic/connections/${id}/test`
  )
}

export function listTables(id: string) {
  return get<{ tables: TableRef[] }>(`/api/v1/semantic/connections/${id}/tables`)
}

export function listColumns(id: string, schema: string, table: string) {
  return get<{ columns: ColumnSchema[] }>(
    `/api/v1/semantic/connections/${id}/columns?schema=${encodeURIComponent(schema)}&table=${encodeURIComponent(table)}`
  )
}

export function generateDraft(id: string, schema: string, table: string) {
  return get<{ yaml: string }>(
    `/api/v1/semantic/connections/${id}/draft?schema=${encodeURIComponent(schema)}&table=${encodeURIComponent(table)}`
  )
}

// ---- 模型 ----

export function listModels() {
  return get<{ models: SemanticModel[] }>('/api/v1/semantic/models')
}

export function getModel(id: string) {
  return get<SemanticModel>(`/api/v1/semantic/models/${id}`)
}

export function createModel(data: Partial<SemanticModel>) {
  return post<SemanticModel>('/api/v1/semantic/models', data)
}

export function updateModel(id: string, data: Partial<SemanticModel>) {
  return put<SemanticModel>(`/api/v1/semantic/models/${id}`, data)
}

export function deleteModel(id: string) {
  return del(`/api/v1/semantic/models/${id}`)
}

export function publishModel(id: string, note = '') {
  return post<{ status: string; version: number }>(`/api/v1/semantic/models/${id}/publish`, { note })
}

export function unpublishModel(id: string) {
  return post(`/api/v1/semantic/models/${id}/unpublish`)
}

export function listVersions(id: string) {
  return get<{ versions: ModelVersion[] }>(`/api/v1/semantic/models/${id}/versions`)
}

export function rollbackModel(id: string, versionId: string) {
  return post<{ status: string; version: number }>(`/api/v1/semantic/models/${id}/rollback`, {
    version_id: versionId
  })
}

export function previewModel(id: string, query: PreviewQuery) {
  return post<PreviewResult>(`/api/v1/semantic/models/${id}/preview`, query)
}

// ---- 数据组 ----

export function listGroups() {
  return get<{ groups: DataGroup[] }>('/api/v1/semantic/groups')
}

export function createGroup(data: Partial<DataGroup>) {
  return post<DataGroup>('/api/v1/semantic/groups', data)
}

export function updateGroup(id: string, data: Partial<DataGroup>) {
  return put<DataGroup>(`/api/v1/semantic/groups/${id}`, data)
}

export function deleteGroup(id: string) {
  return del(`/api/v1/semantic/groups/${id}`)
}

export function setGroupMembers(id: string, userIds: string[]) {
  return put(`/api/v1/semantic/groups/${id}/members`, { user_ids: userIds })
}

export function listGroupMembers(id: string) {
  return get<{ user_ids: string[] }>(`/api/v1/semantic/groups/${id}/members`)
}

export interface GroupUsage {
  model_count: number
  model_names: string[]
}

export function getGroupUsage(id: string) {
  return get<GroupUsage>(`/api/v1/semantic/groups/${id}/usage`)
}

export interface AuditLogEntry {
  id: string
  user_id: string
  action: string
  target: string
  detail?: Record<string, unknown> | null
  created_at: string
}

export function listAudits() {
  return get<{ audit_logs: AuditLogEntry[] }>('/api/v1/semantic/audit')
}
