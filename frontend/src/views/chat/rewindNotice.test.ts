import assert from 'node:assert/strict'
import test from 'node:test'
import { rewindSkipMessage } from './rewindNotice'

const t = (key: string) => key

test('NO_SANDBOX maps to skipNoSandbox', () => {
  assert.equal(rewindSkipMessage('NO_SANDBOX', t), 'chat.rewind.skipNoSandbox')
})

test('NO_CHECKPOINT maps to skipNoCheckpoint', () => {
  assert.equal(rewindSkipMessage('NO_CHECKPOINT', t), 'chat.rewind.skipNoCheckpoint')
})

// A replaced sandbox fail-closes with a 409 rather than truncating, so it
// never arrives here as a skip reason.
test('unknown code falls back to generic skipped copy', () => {
  assert.equal(rewindSkipMessage('WEIRD', t), 'chat.rewind.skipped')
})

test('empty reason returns empty so the UI can skip the workspace notice', () => {
  assert.equal(rewindSkipMessage('', t), '')
})
