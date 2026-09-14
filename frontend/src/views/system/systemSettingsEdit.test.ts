import assert from 'node:assert/strict'
import test from 'node:test'

import { isSettingValueDirty, resolveCurrentSetting } from './systemSettingsEdit.ts'

test('treats scalar edits as dirty', () => {
  assert.equal(isSettingValueDirty(true, false), true)
  assert.equal(isSettingValueDirty(false, false), false)
  assert.equal(isSettingValueDirty('invite', 'open'), true)
})

test('treats list edits as dirty by value, not by reference', () => {
  assert.equal(isSettingValueDirty(['a'], ['a']), false)
  assert.equal(isSettingValueDirty(['a', 'b'], ['a']), true)
  assert.equal(isSettingValueDirty(['a'], ['b']), true)
})

test('resolves the latest setting so a reused row does not keep the pre-save value', () => {
  const stale = { key: 'tenant.auto_create_api_key', value: false }
  const current = { key: 'tenant.auto_create_api_key', value: true }
  const settingsByKey = new Map([[current.key, current]])

  const resolved = resolveCurrentSetting(settingsByKey, stale.key)
  assert.equal(resolved, current)

  // User toggled back to the original loaded value. The stale object looks
  // clean; the canonical post-save object is dirty and must PUT.
  const editValue = false
  assert.equal(isSettingValueDirty(editValue, stale.value), false)
  assert.equal(isSettingValueDirty(editValue, resolved?.value), true)
})

test('high-risk cancel restores the latest saved value, not the first-loaded object', () => {
  const stale = { key: 'auth.registration_mode', value: 'open' }
  const current = { key: 'auth.registration_mode', value: 'invite' }
  const settingsByKey = new Map([[current.key, current]])

  const resolved = resolveCurrentSetting(settingsByKey, stale.key)
  assert.equal(resolved?.value, 'invite')
  assert.notEqual(stale.value, resolved?.value)
})
