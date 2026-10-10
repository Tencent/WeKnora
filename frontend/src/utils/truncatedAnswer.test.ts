import assert from 'node:assert/strict'
import test from 'node:test'
import { isPartialTruncatedAnswer } from './truncatedAnswer'

test('only partial truncated text gets the notice', () => {
  assert.equal(isPartialTruncatedAnswer({ truncated: true, content: 'Partial answer' }), true)
  assert.equal(isPartialTruncatedAnswer({ content: 'Complete answer' }), false)
  assert.equal(isPartialTruncatedAnswer({ truncated: true, is_fallback: true, content: 'Fallback' }), false)
  assert.equal(isPartialTruncatedAnswer({ truncated: true, content: "Sorry, this answer kept hitting the model's per-response output limit before any text was produced. Try narrowing the question, or raise the agent's max_completion_tokens setting." }), false)
})
