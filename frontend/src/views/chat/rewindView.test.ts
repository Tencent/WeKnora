import assert from 'node:assert/strict'
import test from 'node:test'
import {
  canReplaceRewindTranscript,
  keepMessagesThroughRewindPoint,
  rewindableMessageIds,
  resolveRewindAffordance,
  rewindBlockedByOutgoingWork,
  rewindConflictI18nKey,
  rewindHistoryHasMore,
  rewindHttpConflictCode,
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

test('history has more pages when the reload batch fills the limit', () => {
  assert.equal(rewindHistoryHasMore(20, 20), true)
  assert.equal(rewindHistoryHasMore(19, 20), false)
  assert.equal(rewindHistoryHasMore(0, 20), false)
})

test('keeps messages through an assistant rewind point and drops a user point', () => {
  const messages = [
    { id: 'u1', role: 'user' },
    { id: 'a1', role: 'assistant' },
    { id: 'u2', role: 'user' },
    { id: 'a2', role: 'assistant' },
  ]
  assert.deepEqual(
    keepMessagesThroughRewindPoint(messages, 'u2', 'user').map((m) => m.id),
    ['u1', 'a1'],
  )
  assert.deepEqual(
    keepMessagesThroughRewindPoint(messages, 'a1', 'assistant').map((m) => m.id),
    ['u1', 'a1'],
  )
})

test('offers rewind on user and assistant messages unless outgoing work or an incomplete turn', () => {
  const messages = [
    { id: 'u1', role: 'user' },
    { id: 'a1', role: 'assistant', is_completed: true },
  ]
  assert.equal(resolveRewindAffordance(messages, 'u1').canRewind, true)
  assert.equal(resolveRewindAffordance(messages, 'a1').canRewind, true)
  assert.equal(resolveRewindAffordance(messages, 'u1', { embeddedMode: true }).canRewind, false)
  assert.equal(resolveRewindAffordance(messages, 'u1', { outgoingWork: true }).canRewind, false)
  assert.equal(
    resolveRewindAffordance(
      [...messages, { id: 'a2', role: 'assistant', is_completed: false }],
      'u1',
    ).canRewind,
    false,
  )
  assert.equal(resolveRewindAffordance(messages, 'sys', { }).canRewind, false)
})

test('maps rewind HTTP conflicts onto dedicated copy', () => {
  assert.equal(rewindHttpConflictCode({ status: 409, code: 'REWIND_NO_CHECKPOINT' }), 'REWIND_NO_CHECKPOINT')
  assert.equal(rewindHttpConflictCode({ $httpStatus: 409, code: 'REWIND_SOURCE_BUSY' }), 'REWIND_SOURCE_BUSY')
  assert.equal(rewindHttpConflictCode({ status: 500, code: 'REWIND_NO_CHECKPOINT' }), '')
  assert.equal(rewindConflictI18nKey('REWIND_NO_CHECKPOINT'), 'chat.rewind.noCheckpoint')
  assert.equal(rewindConflictI18nKey('REWIND_SANDBOX_REPLACED'), 'chat.rewind.sandboxReplaced')
  assert.equal(rewindConflictI18nKey('REWIND_SOURCE_BUSY'), 'chat.rewind.busy')
  assert.equal(rewindConflictI18nKey(''), 'chat.rewind.busy')
})

// The transcript resolves every row's control from this one set, so it has to
// answer exactly what the per-message check answers — and only walk once.
test('resolves every rewindable id in a single pass', () => {
  const messages = [
    { id: 'u1', role: 'user' },
    { id: 'a1', role: 'assistant', is_completed: true },
    { id: 'sys', role: 'system' },
    { id: '', role: 'user' },
  ]
  assert.deepEqual([...rewindableMessageIds(messages)], ['u1', 'a1'])
  assert.deepEqual([...rewindableMessageIds(messages, { embeddedMode: true })], [])
  assert.deepEqual([...rewindableMessageIds(messages, { outgoingWork: true })], [])
  assert.deepEqual(
    [...rewindableMessageIds([...messages, { id: 'a2', role: 'assistant', is_completed: false }])],
    [],
    'an unfinished turn withdraws the control from the whole transcript',
  )
})
