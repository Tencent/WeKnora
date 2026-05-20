import { get, post, put, del } from '@/utils/request'

export interface MCPService {
  id: string
  tenant_id?: number
  name: string
  description: string
  enabled: boolean
  transport_type: 'sse' | 'http-streamable' | 'stdio'
  url?: string // Optional: required for SSE/HTTP Streamable
  headers?: Record<string, string>
  auth_config?: {
    // Secret fields (api_key, token) are NEVER returned by the server in
    // this shape — they live behind the /credentials subresource. The
    // optional-property typing remains so create-mode payloads can still
    // carry them in the initial POST body.
    api_key?: string
    token?: string
    custom_headers?: Record<string, string>
  }
  advanced_config?: {
    timeout?: number
    retry_count?: number
    retry_delay?: number
  }
  stdio_config?: {
    command: 'uvx' | 'npx' // Command: uvx or npx
    args: string[] // Command arguments array
  }
  env_vars?: Record<string, string> // Environment variables for stdio transport
  is_builtin?: boolean // Whether this is a builtin MCP service
  // Per-field "configured?" map embedded on the main response (server-side
  // dto.MCPServiceResponse.Credentials). Drives the CredentialResource card
  // without a follow-up GET. Absent for builtin services.
  credentials?: Record<McpCredentialField, CredentialFieldMetadata>
  created_at?: string
  updated_at?: string
}

export interface MCPTool {
  name: string
  description: string
  inputSchema: Record<string, any>
  require_approval?: boolean
}

export interface MCPToolApprovalRow {
  id: string
  tenant_id?: number
  service_id: string
  tool_name: string
  require_approval: boolean
}

export interface MCPResource {
  uri: string
  name: string
  description?: string
  mimeType?: string
}

export interface MCPTestResult {
  success: boolean
  message?: string
  tools?: MCPTool[]
  resources?: MCPResource[]
}

// List all MCP services
export async function listMCPServices(): Promise<MCPService[]> {
  const response: any = await get('/api/v1/mcp-services')
  return response.data || []
}

// Get a single MCP service by ID
export async function getMCPService(id: string): Promise<MCPService> {
  const response: any = await get(`/api/v1/mcp-services/${id}`)
  return response.data
}

// Create a new MCP service
export async function createMCPService(data: Partial<MCPService>): Promise<MCPService> {
  const response: any = await post('/api/v1/mcp-services', data)
  return response.data
}

// Update an existing MCP service
export async function updateMCPService(id: string, data: Partial<MCPService>): Promise<MCPService> {
  const response: any = await put(`/api/v1/mcp-services/${id}`, data)
  return response.data
}

// Delete an MCP service
export async function deleteMCPService(id: string): Promise<void> {
  await del(`/api/v1/mcp-services/${id}`)
}

// Test MCP service connection
export async function testMCPService(id: string): Promise<MCPTestResult> {
  const response: any = await post(`/api/v1/mcp-services/${id}/test`, {})
  // 后端返回格式: { success: true, data: MCPTestResult }
  // response interceptor 已经返回了 data，所以 response 就是 { success: true, data: {...} }
  if (response && response.data) {
    return response.data
  }
  // 如果格式不对，尝试直接返回 response（可能是直接返回的数据）
  return response
}

// Get tools from an MCP service
export async function getMCPServiceTools(id: string): Promise<MCPTool[]> {
  const response: any = await get(`/api/v1/mcp-services/${id}/tools`)
  return response.data || []
}

// Get resources from an MCP service
export async function getMCPServiceResources(id: string): Promise<MCPResource[]> {
  const response: any = await get(`/api/v1/mcp-services/${id}/resources`)
  return response.data || []
}

/** Persisted per-tool human-approval flags (issue #1173) */
export async function getMCPToolApprovals(serviceId: string): Promise<MCPToolApprovalRow[]> {
  const response: any = await get(`/api/v1/mcp-services/${serviceId}/tool-approvals`)
  return response.data || []
}

export async function setMCPToolApproval(serviceId: string, toolName: string, requireApproval: boolean): Promise<void> {
  await put(`/api/v1/mcp-services/${serviceId}/tool-approvals/${encodeURIComponent(toolName)}`, {
    require_approval: requireApproval
  })
}

// ----------------------------------------------------------------------------
// Credential subresource (issue #988 follow-up).
//
// Secrets travel through a dedicated /credentials endpoint instead of the
// main MCP PUT body. "Is this configured?" metadata is embedded on the main
// MCPService response (MCPService.credentials), so there is no GET on this
// endpoint — only PUT (write) and DELETE (clear). Both trigger an MCP
// client reconnect server-side.
// ----------------------------------------------------------------------------

export type McpCredentialField = 'api_key' | 'token'

export interface CredentialFieldMetadata {
  configured: boolean
}

export interface McpCredentialsResponse {
  fields: Record<McpCredentialField, CredentialFieldMetadata>
}

export async function putMCPCredentials(
  serviceId: string,
  body: Partial<Record<McpCredentialField, string>>
): Promise<McpCredentialsResponse> {
  const response: any = await put(`/api/v1/mcp-services/${serviceId}/credentials`, body)
  return (response.data ?? response) as McpCredentialsResponse
}

export async function deleteMCPCredentialField(
  serviceId: string,
  field: McpCredentialField
): Promise<void> {
  await del(`/api/v1/mcp-services/${serviceId}/credentials/${field}`)
}

export async function resolveToolApproval(
  pendingId: string,
  body: { decision: 'approve' | 'reject'; modified_args?: Record<string, unknown>; reason?: string }
): Promise<void> {
  await post(`/api/v1/agent/tool-approvals/${encodeURIComponent(pendingId)}`, body)
}

