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
  created_at?: string
  updated_at?: string
}

export interface MCPTool {
  name: string
  description: string
  inputSchema: Record<string, any>
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

