import type { ComposerTranslation } from 'vue-i18n'

const discoveryFields = [
  'mode', 'servers', 'tools', 'total', 'has_more', 'next_cursor', 'next_step',
  'notice', 'status', 'name', 'description', 'input_schema', 'tool_ref', 'server_id',
] as const

function record(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown> : {}
}

function discoveryOverlay(data?: unknown): Record<string, unknown> {
  const extra = record(data)
  const overlay: Record<string, unknown> = {}
  for (const key of discoveryFields) {
    if (key in extra) overlay[key] = extra[key]
  }
  return overlay
}

// Discovery historically stored its complete JSON in output, without tool_data.
// Live SSE copies the whole event envelope onto tool_data; only merge catalog fields.
export function parseMcpDiscovery(output?: string, data?: unknown): Record<string, unknown> {
  let parsed = {}
  try { parsed = record(JSON.parse(output || '')) } catch { /* Error or truncated historical output. */ }
  return { ...parsed, ...discoveryOverlay(data) }
}

export function mcpDiscoveryRows(data: Record<string, unknown>) {
  const source = data.mode === 'list_servers' ? data.servers : data.tools
  if (!Array.isArray(source)) return []
  return source.flatMap((value) => {
    const row = record(value)
    if (typeof row.name !== 'string') return []
    return [{
      name: row.name,
      description: typeof row.description === 'string' ? row.description : '',
      status: typeof row.status === 'string' ? row.status : '',
    }]
  })
}

export function mcpSchemaParameters(schema: unknown) {
  const definition = record(schema)
  const required = Array.isArray(definition.required) ? definition.required : []
  return Object.entries(record(definition.properties)).map(([name, value]) => {
    const property = record(value)
    const type = property.type
    const variants = property.anyOf || property.oneOf
    const types = Array.isArray(variants) ? variants.flatMap((variant) => {
      const type = record(variant).type
      return typeof type === 'string' ? [type] : []
    }) : []
    return {
      name,
      type: typeof type === 'string' ? type : Array.isArray(type) ? type.join(' | ') : types.join(' | '),
      required: required.includes(name),
      description: typeof property.description === 'string' ? property.description : '',
    }
  })
}

export function getMcpToolDisplayType(toolName?: string): 'mcp_discovery' | 'mcp_call' | undefined {
  if (toolName === 'discover_mcp_tools') return 'mcp_discovery'
  if (toolName === 'call_mcp_tool') return 'mcp_call'
  return undefined
}

export function mcpToolResultOutput(event: { tool_name?: string; output?: string; error?: string }): string | undefined {
  if (!getMcpToolDisplayType(event.tool_name)) return event.output
  return event.output || event.error
}

export function getMcpToolTitle(t: ComposerTranslation, event: {
  tool_name?: string; arguments?: unknown; output?: string; tool_data?: unknown; pending?: boolean; success?: boolean
}): string {
  if (!getMcpToolDisplayType(event.tool_name)) return ''
  const data = parseMcpDiscovery(event.output, event.tool_data)
  const args = typeof event.arguments === 'string' ? parseMcpDiscovery(event.arguments) : record(event.arguments)
  const mode = data.mode || args.mode || ('input_schema' in data ? 'describe' : '')
  const keys: Record<string, string> = {
    list_servers: 'agentStream.mcp.listServers',
    list_tools: 'agentStream.mcp.listTools',
    search: 'agentStream.mcp.searchTools',
    describe: 'agentStream.mcp.describeTool',
  }
  const label = t(event.tool_name === 'call_mcp_tool' ? 'agentStream.mcp.callTool' : keys[String(mode)] || 'agentStream.mcp.discoverTools')
  const name = mode === 'describe' ? data.name || args.tool_name : ''
  const title = name && typeof name === 'string' ? `${label}：${name}` : label
  if (event.pending) return t('agentStream.toolStatus.calling', { name: title })
  if (event.success === false) return t('agentStream.toolStatus.calledFailed', { name: title })
  return title
}
