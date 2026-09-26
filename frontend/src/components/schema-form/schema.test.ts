import assert from 'node:assert/strict'
import test from 'node:test'

import {
  REDACTED_SECRET,
  choicesOf,
  inGroup,
  isStoredSecret,
  isVisible,
  orderedFields,
  resolveText,
  schemaAt,
  secretInputText,
  secretInputValue,
  secretPaths,
  validateConfig,
  applyDefaults,
  type ConfigSchema,
  type Translator,
} from './schema'

// Same shape as the backend's web search schema for Zhipu.
const zhipu: ConfigSchema = {
  type: 'object',
  required: ['api_key'],
  properties: {
    proxy_url: { type: 'string', format: 'uri', 'x-group': 'options', 'x-order': 50 },
    api_key: {
      type: 'string', 'x-secret': true, 'x-widget': 'password', 'x-group': 'credentials', 'x-order': 20,
      'x-i18n-keys': { title: 'webSearchSettings.apiKeyLabel' }, title: 'API key',
    },
    extra_config: {
      type: 'object', 'x-group': 'credentials', 'x-order': 40, required: ['content_size'],
      properties: {
        content_size: {
          type: 'string', default: 'medium', title: 'Content size',
          oneOf: [{ const: 'medium', title: 'Medium' }, { const: 'high', title: 'High' }],
        },
      },
    },
  },
}

const tr = (messages: Record<string, string>, locale = 'zh-CN'): Translator => ({
  t: key => messages[key] ?? key,
  te: key => key in messages,
  locale,
})

test('orderedFields sorts by x-order then key and marks required fields', () => {
  const fields = orderedFields(zhipu)
  assert.deepEqual(fields.map(f => f.key), ['api_key', 'extra_config', 'proxy_url'])
  assert.equal(fields[0].required, true)
  assert.equal(fields[2].required, false)
  const tie = orderedFields({ type: 'object', properties: { b: {}, a: {} } })
  assert.deepEqual(tie.map(f => f.key), ['a', 'b'])
})

test('isVisible and inGroup', () => {
  const token: ConfigSchema = { type: 'string', 'x-visible-if': { mode: 'token', port: 8080 } }
  assert.equal(isVisible(token, { mode: 'token', port: 8080 }), true)
  assert.equal(isVisible(token, { mode: 'basic', port: 8080 }), false)
  assert.equal(isVisible(token, undefined), false)
  assert.equal(isVisible({ type: 'string' }, undefined), true)
  assert.equal(inGroup(zhipu.properties!.api_key, 'credentials'), true)
  assert.equal(inGroup(zhipu.properties!.api_key, 'options'), false)
  assert.equal(inGroup(zhipu.properties!.api_key, undefined), true)
  assert.equal(inGroup({ type: 'string' }, ''), true)
})

test('resolveText prefers a known frontend key, then x-i18n, then plain text', () => {
  const apiKey = zhipu.properties!.api_key
  assert.equal(resolveText(apiKey, 'title', tr({ 'webSearchSettings.apiKeyLabel': 'API 密钥' })), 'API 密钥')
  assert.equal(resolveText(apiKey, 'title', tr({})), 'API key')

  const plugin: ConfigSchema = {
    title: 'Region',
    'x-placeholder': 'e.g. bj',
    'x-i18n': { title: { 'zh-CN': '地域' }, placeholder: { 'zh-CN': '例如 bj' } },
  }
  assert.equal(resolveText(plugin, 'title', tr({})), '地域')
  assert.equal(resolveText(plugin, 'title', tr({}, 'zh-TW')), '地域', 'same language falls back')
  assert.equal(resolveText(plugin, 'title', tr({}, 'en-US')), 'Region')
  assert.equal(resolveText(plugin, 'placeholder', tr({}, 'en-US')), 'e.g. bj')
  assert.equal(resolveText(plugin, 'placeholder', tr({})), '例如 bj')
  assert.equal(resolveText(plugin, 'description', tr({})), '')
})

test('applyDefaults fills missing values recursively without overwriting', () => {
  assert.deepEqual(applyDefaults(zhipu, {}), { extra_config: { content_size: 'medium' } })
  assert.deepEqual(
    applyDefaults(zhipu, { proxy_url: 'http://p', extra_config: { content_size: 'high' } }),
    { proxy_url: 'http://p', extra_config: { content_size: 'high' } },
  )
  const input = { extra_config: {} }
  applyDefaults(zhipu, input)
  assert.deepEqual(input, { extra_config: {} }, 'input is not mutated')
})

test('choicesOf reads oneOf, then enum', () => {
  assert.deepEqual(choicesOf(zhipu.properties!.extra_config.properties!.content_size).map(c => c.value), ['medium', 'high'])
  assert.deepEqual(choicesOf({ enum: ['a', 'b'] }).map(c => c.schema.title), ['a', 'b'])
  assert.deepEqual(choicesOf({ type: 'string' }), [])
})

test('secretPaths walks nested objects', () => {
  const s: ConfigSchema = {
    type: 'object',
    properties: {
      token: { type: 'string', 'x-secret': true },
      auth: { type: 'object', properties: { password: { type: 'string', 'x-secret': true } } },
    },
  }
  assert.deepEqual(secretPaths(s), ['auth.password', 'token'])
})

test('validateConfig mirrors the backend rules', () => {
  assert.deepEqual(validateConfig(zhipu, { api_key: 'k', extra_config: { content_size: 'high' } }), [])
  assert.deepEqual(
    validateConfig(zhipu, { extra_config: { content_size: 'huge' }, proxy_url: 'not a url' }),
    [
      { path: 'api_key', code: 'required' },
      { path: 'extra_config.content_size', code: 'enum' },
      { path: 'proxy_url', code: 'format' },
    ],
  )
  assert.deepEqual(
    validateConfig(zhipu, { extra_config: { content_size: 'high' } }, { skipSecrets: true }),
    [],
    'secrets edited elsewhere are not required here',
  )
  assert.deepEqual(validateConfig(zhipu, { api_key: REDACTED_SECRET, extra_config: { content_size: 'high' } }), [])

  const hidden: ConfigSchema = {
    type: 'object',
    required: ['mode', 'token'],
    properties: {
      mode: { type: 'string' },
      token: { type: 'string', minLength: 8, 'x-visible-if': { mode: 'token' } },
      size: { type: 'integer', minimum: 1, maximum: 10 },
    },
  }
  assert.deepEqual(validateConfig(hidden, { mode: 'basic' }), [])
  assert.deepEqual(validateConfig(hidden, { mode: 'token', token: 'short', size: 2.5 }), [
    { path: 'size', code: 'type' },
    { path: 'token', code: 'min_length' },
  ])
  assert.deepEqual(validateConfig(hidden, { mode: 'basic', size: 11 }), [{ path: 'size', code: 'maximum' }])
})

test('schemaAt follows dotted paths', () => {
  assert.equal(schemaAt(zhipu, 'extra_config.content_size')?.default, 'medium')
  assert.equal(schemaAt(zhipu, 'api_key')?.['x-secret'], true)
  assert.equal(schemaAt(zhipu, 'extra_config.missing'), undefined)
  assert.equal(schemaAt(zhipu, 'api_key.deeper'), undefined)
})

test('"$" keys in x-visible-if read the context', () => {
  const s: ConfigSchema = {
    type: 'object',
    required: ['signing_secret'],
    properties: { signing_secret: { type: 'string', 'x-visible-if': { $mode: 'webhook' } } },
  }
  assert.equal(isVisible(s.properties!.signing_secret, {}, { mode: 'webhook' }), true)
  assert.equal(isVisible(s.properties!.signing_secret, { mode: 'webhook' }, { mode: 'websocket' }), false)
  assert.deepEqual(validateConfig(s, {}, { context: { mode: 'websocket' } }), [])
  assert.deepEqual(validateConfig(s, {}, { context: { mode: 'webhook' } }), [{ path: 'signing_secret', code: 'required' }])
})

test('a stored secret is replaced, not appended to', () => {
  const secret: ConfigSchema = { type: 'string', 'x-secret': true }
  const plain: ConfigSchema = { type: 'string' }
  assert.equal(isStoredSecret(secret, REDACTED_SECRET), true)
  assert.equal(isStoredSecret(plain, REDACTED_SECRET), false)
  // The mask shows as an empty field, so typing yields only the new key
  // (not "***newkey", which the backend would save).
  assert.equal(secretInputText(secret, REDACTED_SECRET), '')
  assert.equal(secretInputText(secret, 'typed'), 'typed')
  assert.equal(secretInputText(plain, REDACTED_SECRET), REDACTED_SECRET)
  assert.equal(secretInputText(plain, undefined), '')
  assert.equal(secretInputValue('newkey', true), 'newkey')
  // Emptying the field again keeps the stored secret.
  assert.equal(secretInputValue('', true), REDACTED_SECRET)
  assert.equal(secretInputValue('', false), '')
})
