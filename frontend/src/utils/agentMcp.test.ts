import assert from 'node:assert/strict'
import test from 'node:test'
import { agentUsesMcp } from './agentMcp.ts'

test('MCP capability follows the backend selection rules', () => {
  assert.equal(agentUsesMcp(undefined), false)
  assert.equal(agentUsesMcp(null), false)
  assert.equal(agentUsesMcp({}), false)
  assert.equal(agentUsesMcp({ mcp_selection_mode: 'none', mcp_services: ['svc'] }), false)
  assert.equal(agentUsesMcp({ mcp_selection_mode: 'all' }), true)
  assert.equal(agentUsesMcp({ mcp_selection_mode: 'all', mcp_services: [] }), true)
  assert.equal(agentUsesMcp({ mcp_selection_mode: 'selected' }), false)
  assert.equal(agentUsesMcp({ mcp_selection_mode: 'selected', mcp_services: [] }), false)
  assert.equal(agentUsesMcp({ mcp_selection_mode: 'selected', mcp_services: ['svc'] }), true)
})
