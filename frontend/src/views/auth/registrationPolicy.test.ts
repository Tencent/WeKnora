import assert from 'node:assert/strict'
import test from 'node:test'
import { canRegister } from './registrationPolicy'

for (const [mode, withoutInvite, withInvite] of [
  ['self_serve', true, true],
  ['invite_register', false, true],
  ['invite_only', false, false],
  ['', false, false],
  ['unknown', false, false],
] as const) {
  test(`registration entry for ${mode || 'unavailable configuration'}`, () => {
    assert.equal(canRegister(mode, false), withoutInvite)
    assert.equal(canRegister(mode, true), withInvite)
  })
}

// issue #1866: a fresh deployment (zero accounts) keeps the registration
// entry open even when the mode is closed. The backend exposes this via
// /auth/config's first_registration_open flag.
for (const mode of ['invite_only', 'invite_register', 'unknown'] as const) {
  test(`fresh deployment opens registration for closed mode ${mode || 'unavailable'}`, () => {
    assert.equal(canRegister(mode, false, true), true)
  })
}

// The flag only widens the gate for closed modes; a self_serve deployment
// already shows the entry without it.
test('fresh-deployment flag does not change self_serve behaviour', () => {
  assert.equal(canRegister('self_serve', false, false), true)
  assert.equal(canRegister('self_serve', false, true), true)
})
