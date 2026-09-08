import assert from 'node:assert/strict'
import test from 'node:test'
import type { ComposerTranslation } from 'vue-i18n'
import { getMcpToolDisplayType, getMcpToolTitle, mcpDiscoveryRows, mcpSchemaParameters, parseMcpDiscovery, mcpToolResultOutput } from './mcpToolDisplay'
import { getAgentToolIconName } from './agent-tool-icons'

const t = ((key: string, params?: Record<string, unknown>) => `${key}${params?.name ? ` ${params.name}` : ''}`) as ComposerTranslation

test('MCP proxies have dedicated renderers and icons', () => {
  assert.equal(getMcpToolDisplayType('discover_mcp_tools'), 'mcp_discovery')
  assert.equal(getMcpToolDisplayType('call_mcp_tool'), 'mcp_call')
  assert.equal(getMcpToolDisplayType('mcp_orders_lookup'), undefined) // Existing target reference drawer.
  assert.equal(getAgentToolIconName('discover_mcp_tools'), 'search')
  assert.equal(getAgentToolIconName('call_mcp_tool'), 'terminal')
})

test('old output-only discovery and schema records remain readable', () => {
  const data = parseMcpDiscovery(JSON.stringify({ mode: 'list_tools', tools: [{ name: 'get_log', tool_ref: 'legacy', description: 'Logs' }], total: 2, has_more: true }))
  assert.deepEqual(mcpDiscoveryRows(data), [{ name: 'get_log', description: 'Logs', status: '' }])
  const output = JSON.stringify({ name: 'get_log', input_schema: { type: 'object' } })
  assert.equal(getMcpToolTitle(t, { tool_name: 'discover_mcp_tools', output }), 'agentStream.mcp.describeTool：get_log')
  assert.deepEqual(parseMcpDiscovery('Parameter validation failed'), {})
  assert.deepEqual(mcpDiscoveryRows({ tools: [null, 'broken', { name: 'valid' }] }), [{ name: 'valid', description: '', status: '' }])
  const listed = JSON.stringify({ mode: 'list_tools', tools: [{ name: 'get_log' }], total: 1 })
  const liveEnvelope = { tool_name: 'discover_mcp_tools', success: true, output: listed, error: '', duration_ms: 12, tool_call_id: 'x' }
  assert.deepEqual(mcpDiscoveryRows(parseMcpDiscovery(listed, liveEnvelope)), [{ name: 'get_log', description: '', status: '' }])
  assert.equal(getMcpToolTitle(t, { tool_name: 'discover_mcp_tools', output: listed, tool_data: liveEnvelope }), 'agentStream.mcp.listTools')
})

test('schema preview preserves required and union types without inferring undocumented requirements', () => {
  assert.deepEqual(mcpSchemaParameters({ type: 'object', required: ['start_time'], properties: {
    start_time: { type: 'string', description: 'Start time' },
    limit: { anyOf: [{ type: 'integer' }, { type: 'null' }] },
    flag: { type: ['boolean', 'null'] },
  } }), [
    { name: 'start_time', type: 'string', required: true, description: 'Start time' },
    { name: 'limit', type: 'integer | null', required: false, description: '' },
    { name: 'flag', type: 'boolean | null', required: false, description: '' },
  ])
  assert.deepEqual(mcpSchemaParameters(false), [])
})

test('mode-aware titles handle pending and failed proxy events', () => {
  for (const [mode, suffix] of Object.entries({ list_servers: 'listServers', list_tools: 'listTools', search: 'searchTools', describe: 'describeTool' })) {
    const event = { tool_name: 'discover_mcp_tools', arguments: { mode } }
    assert.equal(getMcpToolTitle(t, event), `agentStream.mcp.${suffix}`)
    assert.match(getMcpToolTitle(t, { ...event, pending: true }), /^agentStream.toolStatus.calling /)
  }
  assert.equal(getMcpToolTitle(t, { tool_name: 'call_mcp_tool', success: false }), 'agentStream.toolStatus.calledFailed agentStream.mcp.callTool')
  assert.equal(mcpToolResultOutput({ tool_name: 'call_mcp_tool', error: 'arguments must be an object' }), 'arguments must be an object')
  assert.equal(mcpToolResultOutput({ tool_name: 'shell_exec', error: 'exited 1' }), undefined)
  assert.equal(mcpToolResultOutput({ tool_name: 'shell_exec', output: 'ok', error: 'exited 1' }), 'ok')
})
