import assert from 'node:assert/strict'
import test from 'node:test'
import { permissionCanEditKB, permissionCanManageKB } from './kbPermission.ts'

test('editor-level grants allow knowledge mutations', () => {
  assert.equal(permissionCanEditKB('owner'), true)
  assert.equal(permissionCanEditKB('admin'), true)
  assert.equal(permissionCanEditKB('editor'), true)
})

test('viewer grant (read-only share) never allows mutations (#3098)', () => {
  assert.equal(permissionCanEditKB('viewer'), false)
})

test('missing permission does not answer the question', () => {
  // Callers must fall through to their own-tenant logic for '' / null.
  assert.equal(permissionCanEditKB(''), false)
  assert.equal(permissionCanEditKB(null), false)
  assert.equal(permissionCanEditKB(undefined), false)
})

test('manage requires admin-level grant', () => {
  assert.equal(permissionCanManageKB('owner'), true)
  assert.equal(permissionCanManageKB('admin'), true)
  assert.equal(permissionCanManageKB('editor'), false)
  assert.equal(permissionCanManageKB('viewer'), false)
  assert.equal(permissionCanManageKB(''), false)
  assert.equal(permissionCanManageKB(undefined), false)
})
