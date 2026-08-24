import assert from 'node:assert/strict'
import test from 'node:test'
import {
  clearMessageStreamRecovery,
  markLatestIncompleteAssistantForRecovery,
  type StreamRecoveryMessage,
} from './stream-recovery-state.ts'

test('marks the latest incomplete assistant message for stream recovery', () => {
  const messages: StreamRecoveryMessage[] = [
    { id: 'user-1', role: 'user', is_completed: true },
    { id: 'assistant-1', role: 'assistant', is_completed: false },
  ]

  const marked = markLatestIncompleteAssistantForRecovery(messages)

  assert.equal(marked, messages[1])
  assert.equal(messages[1].isRecoveringStream, true)
})

test('does not mark a completed assistant message', () => {
  const messages: StreamRecoveryMessage[] = [
    { id: 'assistant-1', role: 'assistant', is_completed: true },
  ]

  assert.equal(markLatestIncompleteAssistantForRecovery(messages), undefined)
  assert.equal(messages[0].isRecoveringStream, undefined)
})

test('does not attach recovery state to a trailing user message', () => {
  const messages: StreamRecoveryMessage[] = [
    { id: 'user-1', role: 'user', is_completed: true },
  ]

  assert.equal(markLatestIncompleteAssistantForRecovery(messages), undefined)
  assert.equal(messages[0].isRecoveringStream, undefined)
})

test('clears recovery state when normal stream processing resumes', () => {
  const message = { id: 'assistant-1', isRecoveringStream: true }

  clearMessageStreamRecovery(message)

  assert.equal(message.isRecoveringStream, false)
})
