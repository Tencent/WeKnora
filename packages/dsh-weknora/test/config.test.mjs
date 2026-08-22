import assert from 'node:assert/strict'
import { test } from 'node:test'

import { ConfigError, normalizeBaseUrl, resolveConfig } from '../dist/config.js'

test('base URLs are normalized to an API root', () => {
  assert.equal(normalizeBaseUrl('https://kb.example.com'), 'https://kb.example.com/api/v1')
  assert.equal(normalizeBaseUrl('https://kb.example.com/'), 'https://kb.example.com/api/v1')
  assert.equal(normalizeBaseUrl('https://kb.example.com/api/v1'), 'https://kb.example.com/api/v1')
  assert.equal(normalizeBaseUrl('https://kb.example.com/api/v2/'), 'https://kb.example.com/api/v2')
})

test('an empty config resolves to documented defaults', () => {
  const config = resolveConfig(undefined)
  assert.equal(config.baseUrl, 'http://localhost:8080/api/v1')
  assert.equal(config.maxResults, 8)
  assert.equal(config.toolPrefix, 'weknora')
  assert.equal(config.resourceUrls, 'handle')
  assert.deepEqual(config.knowledgeBaseIds, [])
  assert.deepEqual(config.tools, { listKnowledgeBases: true, search: true, readDocument: true, ask: true })
})

test('a bad row fails the load and names every violation', () => {
  assert.throws(
    () => resolveConfig({ baseUrl: 'not-a-url', maxResults: -1, resourceUrls: 'nope', toolPrefix: '9bad' }),
    error => {
      assert.ok(error instanceof ConfigError)
      assert.match(error.message, /baseUrl must be an absolute URL/)
      assert.match(error.message, /maxResults must be a positive number/)
      assert.match(error.message, /resourceUrls must be "handle" or "public"/)
      assert.match(error.message, /toolPrefix must match/)
      return true
    },
  )
})

test('disabling every tool is refused', () => {
  assert.throws(
    () => resolveConfig({ tools: { listKnowledgeBases: false, search: false, readDocument: false, ask: false } }),
    /at least one tool must stay enabled/,
  )
})

test('blank strings collapse to unset rather than empty headers', () => {
  const config = resolveConfig({ apiKey: '   ', tenantId: '', agentId: 'agent-7' })
  assert.equal(config.apiKey, undefined)
  assert.equal(config.tenantId, undefined)
  assert.equal(config.agentId, 'agent-7')
})
