import assert from 'node:assert/strict'
import test from 'node:test'
import { validateMCPHeaders, hasDynamicMCPHeaders } from './mcpHeaderTemplate'

test('arbitrary business header names, fallbacks, prefixes and escaped braces are accepted', () => {
  assert.equal(validateMCPHeaders([
    { key: 'AAA', value: '{{request.headers.X-AAA}}' },
    { key: 'Identity', value: 'employee:{{user.email ?? external.user_id ?? im.user_id}}' },
    { key: 'Literal', value: String.raw`\{{untouched}}` },
  ]), null)
  assert.equal(hasDynamicMCPHeaders({ AAA: '{{request.headers.X-New-Department}}' }), true)
  assert.equal(hasDynamicMCPHeaders({ AAA: String.raw`\{{literal}}` }), false)
})

test('malformed templates, credential forwarding and case-insensitive duplicates are rejected', () => {
  for (const value of ['{{user.typo}}', '{{request.headers.Cookie}}', '{{request.headers.X-External-User-ID}}', '{{request.headers.X-User-ID}}', '{{request.headers.X-Workspace-ID}}', '{{request.headers.X-Auth-Token}}', '{{env.SECRET}}', '{{user.email', '{{user.id ??}}']) {
    assert.ok(validateMCPHeaders([{ key: 'AAA', value }]), value)
  }
  assert.ok(validateMCPHeaders([{ key: 'Authorization', value: '{{user.id}}' }]))
  assert.ok(validateMCPHeaders([{ key: 'Secret-Key', value: '{{user.id}}' }], 'Secret-Key'))
  assert.ok(validateMCPHeaders([{ key: 'AAA', value: 'one' }, { key: 'aaa', value: 'two' }]))
  assert.ok(validateMCPHeaders([{ key: 'AAA', value: 'secret\r\nvalue' }]))
})
