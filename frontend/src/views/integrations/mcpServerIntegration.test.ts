import assert from 'node:assert/strict'
import test from 'node:test'
import {
  buildClaudeCodeCommand,
  buildHttpClientSnippet,
  buildMcpEndpointUrl,
  buildStdioBridgeSnippet,
  groupTools,
  mcpServerKey,
  MCP_TOKEN_PLACEHOLDER,
} from './mcpServerIntegration'

test('buildMcpEndpointUrl strips the api prefix and joins the endpoint path', () => {
  assert.equal(buildMcpEndpointUrl('https://kb.example.com/api/v1', '/mcp/abc'), 'https://kb.example.com/mcp/abc')
  assert.equal(buildMcpEndpointUrl('https://kb.example.com/api/v1/', 'mcp/abc'), 'https://kb.example.com/mcp/abc')
  assert.equal(buildMcpEndpointUrl('http://localhost:8080', '/mcp/x'), 'http://localhost:8080/mcp/x')
})

test('mcpServerKey slugs the endpoint name', () => {
  assert.equal(mcpServerKey('Docs Bot!'), 'weknora-docs-bot')
  assert.equal(mcpServerKey('  '), 'weknora')
  assert.equal(mcpServerKey('产品知识库'), 'weknora')
  assert.equal(mcpServerKey('产品知识库', '3f9a2c1e-aaaa'), 'weknora-3f9a2c1e')
  assert.notEqual(mcpServerKey('知识库A', 'id-one'), mcpServerKey('知识库B', 'id-two'))
})

test('http snippet carries url and bearer header', () => {
  const parsed = JSON.parse(buildHttpClientSnippet('Docs', 'https://h/mcp/1', 'mcp_t'))
  assert.deepEqual(parsed, {
    mcpServers: { 'weknora-docs': { url: 'https://h/mcp/1', headers: { Authorization: 'Bearer mcp_t' } } },
  })
})

test('snippets fall back to a placeholder when no token is known', () => {
  assert.match(buildHttpClientSnippet('a', 'u', ''), new RegExp(MCP_TOKEN_PLACEHOLDER))
  assert.match(buildClaudeCodeCommand('a', 'u', ''), new RegExp(MCP_TOKEN_PLACEHOLDER))
  const bridge = JSON.parse(buildStdioBridgeSnippet('a', 'https://h/mcp/1', ''))
  assert.equal(bridge.mcpServers['weknora-a'].command, 'npx')
  assert.ok(bridge.mcpServers['weknora-a'].args.includes('https://h/mcp/1'))
})

test('claude code command uses the http transport', () => {
  assert.equal(
    buildClaudeCodeCommand('Docs', 'https://h/mcp/1', 'mcp_t'),
    'claude mcp add --transport http weknora-docs https://h/mcp/1 --header "Authorization: Bearer mcp_t"',
  )
})

test('groupTools keeps backend order and drops empty groups', () => {
  const grouped = groupTools(['retrieve', 'chat', 'wiki', 'ingest'], [
    { name: 'ask', group: 'chat', destructive: false },
    { name: 'search_knowledge', group: 'retrieve', destructive: false },
    { name: 'delete_document', group: 'ingest', destructive: true },
  ])
  assert.deepEqual(grouped.map((g) => g.group), ['retrieve', 'chat', 'ingest'])
  assert.deepEqual(grouped[0].tools.map((t) => t.name), ['search_knowledge'])
})
