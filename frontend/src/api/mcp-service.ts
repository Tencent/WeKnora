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
    // Authentication strategy. Empty/absent means none. "oauth" enables the
    // per-user OAuth2 authorization-code flow (zero-config: discovery +
    // dynamic client registration).
    auth_type?: '' | 'api_key' | 'bearer' | 'oauth'
    // Secret fields (api_key, token) are NEVER returned by the server in
    // this shape — they live behind the /credentials subresource. The
    // optional-property typing remains so create-mode payloads can still
    // carry them in the initial POST body.
    api_key?: string
    // Header name carrying api_key when auth_type is "api_key". Non-secret;
    // empty defaults to "X-API-Key". Lets services expecting the key in a
    // different header (e.g. raw token in "Authorization") work.
    api_key_header?: string
    token?: string
    custom_headers?: Record<string, string>
    // OAuth-only, non-secret configuration.
    scopes?: string[]
    auth_server_metadata_url?: string
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
  description?: string
  // Set when the server requires OAuth (RFC 9728) but the service was not
  // configured for it — the UI guides the user to switch to OAuth 2.0.
  oauth_required?: boolean
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

// ----------------------------------------------------------------------------
// Per-user OAuth2 authorization-code flow.
//
// The user authorizes a service once; the backend stores their access/refresh
// token (per tenant + user + service) and refreshes it transparently. The
// callback is a public backend route that the third-party authorization
// server redirects to.
// ----------------------------------------------------------------------------

// Path of the public backend OAuth callback (registered outside /mcp-services
// to avoid a route conflict, and allow-listed for no-auth in the backend).
export const MCP_OAUTH_CALLBACK_PATH = '/api/v1/mcp-oauth/callback'

// Begin authorization for the current user. Returns the URL to open in a popup.
export async function getMCPOAuthAuthorizeURL(
  serviceId: string,
  body: { redirect_uri: string; frontend_redirect?: string }
): Promise<string> {
  const response: any = await post(`/api/v1/mcp-services/${serviceId}/oauth/authorize-url`, body)
  return (response.data ?? response)?.authorization_url ?? ''
}

// Whether the current user has authorized this service.
export async function getMCPOAuthStatus(serviceId: string): Promise<boolean> {
  const response: any = await get(`/api/v1/mcp-services/${serviceId}/oauth/status`)
  return Boolean((response.data ?? response)?.authorized)
}

// Revoke the current user's token (forces re-authorization).
export async function revokeMCPOAuthToken(serviceId: string): Promise<void> {
  await del(`/api/v1/mcp-services/${serviceId}/oauth/token`)
}

export async function resolveToolApproval(
  pendingId: string,
  body: { decision: 'approve' | 'reject'; modified_args?: Record<string, unknown>; reason?: string }
): Promise<void> {
  await post(`/api/v1/agent/tool-approvals/${encodeURIComponent(pendingId)}`, body)
}
