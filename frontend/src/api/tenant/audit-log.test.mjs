import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

const api = readFileSync(new URL('./audit-log.ts', import.meta.url), 'utf8')
const tenantMembers = readFileSync(
  new URL('../../views/settings/TenantMembers.vue', import.meta.url),
  'utf8',
)

test('tenant audit API forwards the sanitized command search query', () => {
  assert.match(api, /search\?: string/)
  assert.match(api, /if \(params\.search\) qs\.append\('search', params\.search\)/)
})

test('audit pagination keeps the applied search stable until an explicit reload', () => {
  assert.match(tenantMembers, /if \(reset\) auditAppliedSearch\.value = auditSearch\.value\.trim\(\)/)
  assert.match(tenantMembers, /search: auditAppliedSearch\.value \|\| undefined/)
  assert.match(tenantMembers, /row\.action === 'sandbox\.terminal_command'/)
})
