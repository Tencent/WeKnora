import assert from 'node:assert/strict'
import test from 'node:test'

import { isOAuthRef, validateConfig, type ConfigSchema } from './schema'
import { dependencyKey, isOAuthResult, valueAt, type SchemaFormSource } from './source'

const schema: ConfigSchema = {
  type: 'object',
  required: ['account'],
  properties: {
    account: { type: 'string', 'x-oauth': { authorizeUrl: 'https://auth.example.com/authorize' } },
    project: { type: 'string', enum: ['a'], 'x-options': { name: 'projects', dependsOn: ['account'] } },
  },
}

test('x-oauth fields hold a connection reference; x-options choices are the plugin\'s to check', () => {
  assert.equal(isOAuthRef('oauth:0b7c'), true)
  for (const v of ['oauth:', 'token', 1, undefined]) assert.equal(isOAuthRef(v), false)
  assert.deepEqual(validateConfig(schema, { account: 'oauth:1', project: 'listed-by-plugin' }), [])
  assert.deepEqual(validateConfig(schema, {}), [{ path: 'account', code: 'required' }])
  assert.deepEqual(validateConfig(schema, { account: 'a-token' }), [{ path: 'account', code: 'format' }])
})

test('OAuth results count only from the popup, the callback origin and the same state', () => {
  const popup = {}
  const start = { authorizeUrl: 'https://auth.example.com', state: 's1', redirectUri: 'https://wk.example.com/api/v1/plugin-oauth/callback' }
  const data = { type: 'weknora-plugin-oauth', state: 's1', ok: true, connection: 'oauth:1' }
  const ev = { data, origin: 'https://wk.example.com', source: popup }
  assert.equal(isOAuthResult(ev, popup, start), true)
  assert.equal(isOAuthResult({ ...ev, source: {} }, popup, start), false)
  assert.equal(isOAuthResult({ ...ev, origin: 'https://evil.example.com' }, popup, start), false)
  assert.equal(isOAuthResult({ ...ev, data: { ...data, state: 's2' } }, popup, start), false)
  assert.equal(isOAuthResult({ ...ev, data: { ...data, type: 'other' } }, popup, start), false)
  assert.equal(isOAuthResult({ ...ev, data: null }, popup, start), false)
  assert.equal(isOAuthResult(ev, popup, { ...start, redirectUri: 'not a url' }), false)
})

test('choices reload only when the fields they depend on change', () => {
  const values: Record<string, unknown> = { auth: { site: 'a' }, other: 1 }
  const source: SchemaFormSource = {
    dependency: path => valueAt(values, path),
    options: async () => [],
    startOAuth: async () => ({ authorizeUrl: '', state: '', redirectUri: '' }),
  }
  const spec = { name: 'projects', dependsOn: ['auth.site', 'missing'] }
  const before = dependencyKey(source, spec)
  values.other = 2
  assert.equal(dependencyKey(source, spec), before)
  values.auth = { site: 'b' }
  assert.notEqual(dependencyKey(source, spec), before)
  assert.equal(dependencyKey(source, { name: 'projects' }), '')
  assert.equal(dependencyKey(undefined, spec), '')
  assert.equal(valueAt({ a: [1] }, 'a.0'), undefined)
})
