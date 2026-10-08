import { get, post, put, del } from '@/utils/request'

export type McpEndpointToolGroup = 'retrieve' | 'chat' | 'wiki' | 'ingest'

export interface McpEndpoint {
  id: string
  tenant_id: number
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

export function listMcpEndpoints() {
  return get<Envelope<McpEndpoint[]>>('/api/v1/mcp-endpoints')
}

export function getMcpEndpointToolCatalog() {
  return get<Envelope<McpEndpointToolCatalog>>('/api/v1/mcp-endpoints/tools')
}

export function createMcpEndpoint(data: McpEndpointPayload) {
  return post<Envelope<McpEndpoint>>('/api/v1/mcp-endpoints', data)
}

export function updateMcpEndpoint(id: string, data: McpEndpointPayload) {
  return put<Envelope<McpEndpoint>>(`/api/v1/mcp-endpoints/${id}`, data)
}

export function deleteMcpEndpoint(id: string) {
  return del(`/api/v1/mcp-endpoints/${id}`)
}

export function rotateMcpEndpointToken(id: string) {
  return post<Envelope<McpEndpoint>>(`/api/v1/mcp-endpoints/${id}/rotate-token`, {})
}
