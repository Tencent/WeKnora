import assert from 'node:assert/strict'
import test from 'node:test'
import { isPartialTruncatedAnswer, isFinalPartialTruncatedAnswer } from './truncatedAnswer'

test('only partial truncated text gets the notice', () => {
  assert.equal(isPartialTruncatedAnswer({ truncated: true, content: 'Partial answer' }), true)
  assert.equal(isPartialTruncatedAnswer({ content: 'Complete answer' }), false)
  assert.equal(isPartialTruncatedAnswer({ truncated: true, is_fallback: true, content: 'Fallback' }), false)
  assert.equal(isPartialTruncatedAnswer({ truncated: true, content: "Sorry, this answer kept hitting the model's per-response output limit before any text was produced. Try narrowing the question, or raise the agent's max_completion_tokens setting." }), false)
})

test('a continued raw history stream only warns for its last visible partial answer', () => {
  const partial = { type: 'answer', truncated: true, content: 'Partial' };
  const stream = [partial];
  assert.equal(isFinalPartialTruncatedAnswer(partial, stream), true);
  const complete = { type: 'answer', truncated: false, content: 'Complete' };
  stream.push(complete);
  assert.equal(isFinalPartialTruncatedAnswer(partial, stream), false);
  assert.equal(isFinalPartialTruncatedAnswer(complete, stream), false);
})
