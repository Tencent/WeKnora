import assert from 'node:assert/strict'
import test from 'node:test'
import {
  canReplaceRewindTranscript,
  rewindBlockedByOutgoingWork,
  rewindPrefillText,
  shouldApplyRewindLocally,
} from './rewindView'

test('applies rewind UI only while still on the source session', () => {
  assert.equal(shouldApplyRewindLocally('sess-1', 'sess-1'), true)
  assert.equal(shouldApplyRewindLocally('sess-2', 'sess-1'), false)
  assert.equal(shouldApplyRewindLocally('', 'sess-1'), false)
})

test('prefills only the user rewind point', () => {
  assert.equal(rewindPrefillText('user', 'rewrite this'), 'rewrite this')
  assert.equal(rewindPrefillText('assistant', 'keep this answer'), '')
  assert.equal(rewindPrefillText('user', undefined), '')
})

test('blocks rewind while a send, stream, or IM recover is in flight', () => {
  assert.equal(rewindBlockedByOutgoingWork({}), false)
  assert.equal(rewindBlockedByOutgoingWork({ isReplying: true }), true)
  assert.equal(rewindBlockedByOutgoingWork({ isStreaming: true }), true)
  assert.equal(rewindBlockedByOutgoingWork({ isRecovering: true }), true)
})

test('replaces the transcript only after a successful reload on the source session', () => {
  assert.equal(canReplaceRewindTranscript('sess-1', 'sess-1', undefined), true)
  assert.equal(canReplaceRewindTranscript('sess-1', 'sess-1', new Error('network')), false)
  assert.equal(canReplaceRewindTranscript('sess-2', 'sess-1', undefined), false)
})
