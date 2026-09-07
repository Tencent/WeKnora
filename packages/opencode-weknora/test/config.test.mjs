import assert from 'node:assert/strict'
import { test } from 'node:test'

import { ConfigError, configFromEnv, normalizeBaseUrl, resolveConfig } from '../dist/config.js'

test('resolveConfig applies the documented defaults', () => {
  const config = resolveConfig(undefined)
  assert.equal(config.baseUrl, 'http://localhost:8080/api/v1')
  assert.deepEqual(config.knowledgeBaseIds, [])
  assert.equal(config.maxResults, 8)
  assert.equal(config.maxChunkChars, 1200)
  assert.equal(config.resourceUrls, 'public')
  assert.equal(config.toolPrefix, 'weknora')
  assert.deepEqual(config.tools, { listKnowledgeBases: true, search: true, readDocument: true, ask: true })
})

test('normalizeBaseUrl appends the api root once', () => {
  assert.equal(normalizeBaseUrl('https://weknora.example.com'), 'https://weknora.example.com/api/v1')
  assert.equal(normalizeBaseUrl('https://weknora.example.com/'), 'https://weknora.example.com/api/v1')
  assert.equal(normalizeBaseUrl('http://localhost:8080/api/v1'), 'http://localhost:8080/api/v1')
})

test('resolveConfig reports every violation instead of failing late', () => {
  assert.throws(() => resolveConfig({ baseUrl: '  ', maxResults: -1, toolPrefix: '1bad', resourceUrls: 'both' }), (error) => {
    assert.ok(error instanceof ConfigError)
    const text = String(error.message)
    for (const fragment of ['baseUrl', 'maxResults', 'toolPrefix', 'resourceUrls']) assert.ok(text.includes(fragment))
    return true
  })
})

test('configFromEnv reads the WEKNORA_* variables', () => {
  const config = configFromEnv({
    WEKNORA_BASE_URL: 'https://weknora.example.com',
    WEKNORA_API_KEY: 'sk-test',
    WEKNORA_TENANT_ID: '10000',
    WEKNORA_KNOWLEDGE_BASE_IDS: 'kb-a, kb-b ,,',
    WEKNORA_TOOL_PREFIX: 'team_kb',
  })
  assert.equal(config.baseUrl, 'https://weknora.example.com')
  assert.equal(config.apiKey, 'sk-test')
  assert.equal(config.tenantId, '10000')
  assert.deepEqual(config.knowledgeBaseIds, ['kb-a', 'kb-b'])
  assert.equal(config.toolPrefix, 'team_kb')
  // An invalid enum value is ignored rather than failing the whole layer.
  assert.equal(configFromEnv({ WEKNORA_RESOURCE_URLS: 'both' }).resourceUrls, undefined)
})

test('plugin options merge over the environment', () => {
  const config = resolveConfig({ ...configFromEnv({ WEKNORA_API_KEY: 'sk-env' }), apiKey: 'sk-options' })
  assert.equal(config.apiKey, 'sk-options')
})
