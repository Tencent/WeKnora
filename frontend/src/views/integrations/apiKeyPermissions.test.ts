import assert from 'node:assert/strict'
import { test } from 'node:test'
import { canAssignKBPermission, cloneAPIKeyKBPermissions, selectAPIKeyKBs, type APIKeyKnowledgeBaseOption } from './apiKeyScope.ts'

test('empty granular permissions never become unrestricted legacy permissions', () => {
  assert.equal(cloneAPIKeyKBPermissions(undefined), null)
  assert.equal(cloneAPIKeyKBPermissions(null), null)
  assert.deepEqual(cloneAPIKeyKBPermissions({}), {})
  assert.deepEqual(selectAPIKeyKBs([], { a: 'manage' }), {})
})

test('editing and selecting KBs preserves existing grants without sharing mutable state', () => {
  const original = { a: 'read', b: 'write' } as const
  const copy = cloneAPIKeyKBPermissions(original)!
  copy.a = 'manage'
  assert.equal(original.a, 'read')
  assert.deepEqual(selectAPIKeyKBs(['b', 'c'], original), { b: 'write', c: 'read' })
})

test('assignable permissions intersect key capabilities and current share ceiling', () => {
  const readOnly: APIKeyKnowledgeBaseOption = { id: 'a', name: 'Shared A', shared: true, maxPermission: 'read' }
  const editable: APIKeyKnowledgeBaseOption = { ...readOnly, maxPermission: 'manage' }
  assert.equal(canAssignKBPermission('write', readOnly, ['ingest']), false)
  assert.equal(canAssignKBPermission('manage', readOnly, ['manage_kbs']), false)
  assert.equal(canAssignKBPermission('read', readOnly, ['chat']), true)
  assert.equal(canAssignKBPermission('write', editable, ['retrieve']), false)
  assert.equal(canAssignKBPermission('write', editable, ['ingest']), true)
  assert.equal(canAssignKBPermission('manage', editable, ['ingest']), false)
  assert.equal(canAssignKBPermission('manage', editable, ['manage_kbs']), true)
  assert.equal(canAssignKBPermission('read', undefined, ['retrieve']), false)
})
