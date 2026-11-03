import assert from 'node:assert/strict'
import test from 'node:test'

import { createHoldToTalk, isVoiceHotkeyDown, voiceHotkeyLabel } from './voiceHotkey.ts'

const key = (code: string, extra: Partial<KeyboardEvent> = {}) => {
  let prevented = false
  return {
    code,
    altKey: false,
    ctrlKey: false,
    metaKey: false,
    shiftKey: false,
    repeat: false,
    isComposing: false,
    ...extra,
    preventDefault() { prevented = true },
    get prevented() { return prevented },
  }
}

function harness(enabled = true) {
  const calls: string[] = []
  const hold = createHoldToTalk({
    enabled: () => enabled,
    onStart: () => calls.push('start'),
    onStop: () => calls.push('stop'),
  })
  return { hold, calls }
}

test('only Alt+Space without other modifiers matches', () => {
  assert.equal(isVoiceHotkeyDown(key('Space', { altKey: true })), true)
  assert.equal(isVoiceHotkeyDown(key('Space')), false)
  assert.equal(isVoiceHotkeyDown(key('Space', { altKey: true, metaKey: true })), false)
  assert.equal(isVoiceHotkeyDown(key('Space', { altKey: true, ctrlKey: true })), false)
  assert.equal(isVoiceHotkeyDown(key('Space', { altKey: true, shiftKey: true })), false)
  assert.equal(isVoiceHotkeyDown(key('Space', { altKey: true, isComposing: true })), false)
  assert.equal(isVoiceHotkeyDown(key('KeyV', { altKey: true })), false)
})

test('hold starts once, swallows auto-repeat, stops on Space release', () => {
  const { hold, calls } = harness()
  const down = key('Space', { altKey: true })
  hold.keydown(down)
  assert.equal(down.prevented, true)
  const repeat = key('Space', { altKey: true, repeat: true })
  hold.keydown(repeat)
  assert.equal(repeat.prevented, true)
  hold.keyup(key('Space'))
  assert.deepEqual(calls, ['start', 'stop'])
  assert.equal(hold.holding, false)
})

test('releasing Alt first also stops', () => {
  const { hold, calls } = harness()
  hold.keydown(key('Space', { altKey: true }))
  hold.keyup(key('AltLeft'))
  hold.keyup(key('Space'))
  assert.deepEqual(calls, ['start', 'stop'])
})

test('release() stops a held press once (window blur)', () => {
  const { hold, calls } = harness()
  hold.keydown(key('Space', { altKey: true }))
  hold.release()
  hold.release()
  assert.deepEqual(calls, ['start', 'stop'])
})

test('disabled hotkey leaves the event alone', () => {
  const { hold, calls } = harness(false)
  const down = key('Space', { altKey: true })
  hold.keydown(down)
  hold.keyup(key('Space'))
  assert.equal(down.prevented, false)
  assert.deepEqual(calls, [])
})

test('a repeat without a tracked press does not start', () => {
  const { hold, calls } = harness()
  hold.keydown(key('Space', { altKey: true, repeat: true }))
  assert.deepEqual(calls, [])
})

test('label follows the platform', () => {
  assert.equal(voiceHotkeyLabel('MacIntel'), '⌥ Space')
  assert.equal(voiceHotkeyLabel('Win32'), 'Alt+Space')
})
