import assert from 'node:assert/strict'
import test from 'node:test'
import { normalizeMcpBaseUrl } from './mcpBaseUrl'

test('normalizeMcpBaseUrl keeps origin and path prefix without trailing slash', () => {
  assert.equal(normalizeMcpBaseUrl('https://mcp.example.com/'), 'https://mcp.example.com')
  assert.equal(normalizeMcpBaseUrl('https://mcp.example.com/kb/'), 'https://mcp.example.com/kb')
  assert.equal(normalizeMcpBaseUrl(' http://localhost:8080 '), 'http://localhost:8080')
})

test('normalizeMcpBaseUrl rejects empty and non-http values', () => {
  assert.equal(normalizeMcpBaseUrl(''), '')
  assert.equal(normalizeMcpBaseUrl(undefined), '')
  assert.equal(normalizeMcpBaseUrl('ftp://x'), '')
  assert.equal(normalizeMcpBaseUrl('javascript:alert(1)'), '')
  assert.equal(normalizeMcpBaseUrl('not a url'), '')
})
