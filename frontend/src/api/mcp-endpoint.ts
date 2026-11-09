import { get, post, put, del } from '@/utils/request'

export type McpEndpointToolGroup = 'retrieve' | 'chat' | 'wiki' | 'ingest'

/** Space endpoints are admin-published; personal endpoints act as their owner. */
export type McpEndpointKind = 'space' | 'personal'

export interface McpEndpoint {
  id: string
  tenant_id: number
  kind: McpEndpointKind
  name: string
  description: string
  enabled: boolean
  token_hint: string
  knowledge_base_ids: string[]
  tools: string[]
  default_agent_id: string
  rate_limit_per_minute: number
  /** Public path the MCP client connects to, relative to the server origin. */
  path: string
  last_used_at?: string | null
  created_at: string
  updated_at: string
  /** Only present on create / rotate responses; shown once. */
  token?: string
}

export interface McpEndpointToolDefinition {
  name: string
  group: McpEndpointToolGroup
  destructive: boolean
}

export interface McpEndpointToolCatalog {
  groups: McpEndpointToolGroup[]
  tools: McpEndpointToolDefinition[]
  default_tools: string[]
}

export interface McpEndpointPayload {
  name?: string
  description?: string
  enabled?: boolean
  knowledge_base_ids?: string[]
  tools?: string[]
  default_agent_id?: string
  rate_limit_per_minute?: number
}

interface Envelope<T> {
  success: boolean
  data: T
}

export interface McpEndpointSettings {
  personal_rate_limit_per_minute: number
}

function basePath(kind: McpEndpointKind) {
  return kind === 'personal' ? '/api/v1/mcp-personal-endpoints' : '/api/v1/mcp-endpoints'
}

export function listMcpEndpoints(kind: McpEndpointKind = 'space') {
  return get<Envelope<McpEndpoint[]>>(basePath(kind))
}

export function getMcpEndpointToolCatalog() {
  return get<Envelope<McpEndpointToolCatalog>>('/api/v1/mcp-endpoints/tools')
}

export function createMcpEndpoint(data: McpEndpointPayload, kind: McpEndpointKind = 'space') {
  return post<Envelope<McpEndpoint>>(basePath(kind), data)
}

export function updateMcpEndpoint(id: string, data: McpEndpointPayload, kind: McpEndpointKind = 'space') {
  return put<Envelope<McpEndpoint>>(`${basePath(kind)}/${id}`, data)
}

export function deleteMcpEndpoint(id: string, kind: McpEndpointKind = 'space') {
  return del(`${basePath(kind)}/${id}`)
}

export function rotateMcpEndpointToken(id: string, kind: McpEndpointKind = 'space') {
  return post<Envelope<McpEndpoint>>(`${basePath(kind)}/${id}/rotate-token`, {})
}

export function getMcpEndpointSettings() {
  return get<Envelope<McpEndpointSettings>>('/api/v1/mcp-endpoints/settings')
}

export function updateMcpEndpointSettings(data: McpEndpointSettings) {
  return put<Envelope<McpEndpointSettings>>('/api/v1/mcp-endpoints/settings', data)
}
