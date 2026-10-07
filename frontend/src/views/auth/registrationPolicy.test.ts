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
