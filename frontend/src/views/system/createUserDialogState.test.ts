import assert from 'node:assert/strict'
import test from 'node:test'

import {
  applyCreateUserResponse,
  captureLockedEscape,
  resolveCreateUserView,
  shouldAcceptCreateUserSubmit,
  type CreateUserReveal,
} from './createUserDialogState.ts'

const identity = { username: 'alice', email: 'alice@example.com' }

test('reveal wins whenever the server minted a one-time password', () => {
  const view = resolveCreateUserView(
    { generated_password: 'OnceOnly9', idempotent: false },
    identity,
    true,
  )
  assert.deepEqual(view, {
    kind: 'reveal',
    username: 'alice',
    email: 'alice@example.com',
    generatedPassword: 'OnceOnly9',
  })
})

test('idempotent flag is the 200 retry signal when no password was minted', () => {
  assert.deepEqual(
    resolveCreateUserView({ idempotent: true }, identity, true),
    { kind: 'idempotent' },
  )
  assert.deepEqual(
    resolveCreateUserView({ idempotent: true }, identity, false),
    { kind: 'idempotent' },
  )
})

test('supplied-password create without the idempotent flag is a real create', () => {
  assert.deepEqual(
    resolveCreateUserView({}, identity, false),
    { kind: 'created' },
  )
})

test('auto-generate create missing generated_password is not treated as idempotent', () => {
  assert.deepEqual(
    resolveCreateUserView({}, identity, true),
    { kind: 'missingPassword' },
  )
})

test('a later idempotent response does not close an open reveal', () => {
  const current: CreateUserReveal = {
    username: 'alice',
    email: 'alice@example.com',
    generatedPassword: 'OnceOnly9',
  }
  const next = applyCreateUserResponse(current, { kind: 'idempotent' })
  assert.equal(next.close, false)
  assert.equal(next.notice, null)
  assert.equal(next.success, current)
  assert.equal(next.success?.generatedPassword, 'OnceOnly9')
})

test('applyCreateUserResponse opens reveal and keeps the dialog open', () => {
  const next = applyCreateUserResponse(
    null,
    {
      kind: 'reveal',
      username: 'alice',
      email: 'alice@example.com',
      generatedPassword: 'OnceOnly9',
    },
  )
  assert.equal(next.close, false)
  assert.equal(next.notice, 'reveal')
  assert.equal(next.success?.generatedPassword, 'OnceOnly9')
})

test('submit is rejected while validating/in-flight or while revealing', () => {
  assert.equal(shouldAcceptCreateUserSubmit(false, false), true)
  assert.equal(shouldAcceptCreateUserSubmit(true, false), false)
  assert.equal(shouldAcceptCreateUserSubmit(false, true), false)
})

test('locked visible dialog captures Escape before Settings can unmount', () => {
  let stopped = false
  const event = {
    key: 'Escape',
    stopImmediatePropagation() {
      stopped = true
    },
  }
  assert.equal(captureLockedEscape(true, true, event), true)
  assert.equal(stopped, true)

  stopped = false
  assert.equal(captureLockedEscape(true, false, event), false)
  assert.equal(stopped, false)
  assert.equal(captureLockedEscape(false, true, { key: 'Escape', stopImmediatePropagation() { stopped = true } }), false)
})
