import assert from 'node:assert/strict'
import test from 'node:test'
import { canSeedAnswerEvent } from './agentAnswerSeed.ts'

const answer = (content = '', extra: Record<string, unknown> = {}) => ({ type: 'answer', content, ...extra })

test('the first answer event adopts text a non-agent render produced', () => {
  const event = answer()
  assert.equal(canSeedAnswerEvent([event], event, 'already rendered'), true)
  // No stream at all is the same case.
  assert.equal(canSeedAnswerEvent(undefined, event, 'already rendered'), true)
})

test('a later answer event never adopts it, which is the cross-round duplication', () => {
  // The reported sequence: answer(r1) streams, no tool_call passes, answer(r2)
  // starts. message.content is r1's text, recomposed from the stream.
  const first = answer('1. first point', { event_id: 'r1' })
  const second = answer('', { event_id: 'r2' })
  assert.equal(canSeedAnswerEvent([first, second], second, '1. first point'), false)
})

test('a superseded earlier event still proves the text came from the stream', () => {
  const dropped = answer('preamble', { event_id: 'r1', superseded: true })
  const fresh = answer('', { event_id: 'r2' })
  assert.equal(canSeedAnswerEvent([dropped, fresh], fresh, 'preamble'), false)
})

test('events of other types do not stand in the way of the first seed', () => {
  const fresh = answer('', { event_id: 'r1' })
  const stream = [{ type: 'thinking', content: 'pondering' }, { type: 'tool_call', content: 'searching' }, fresh]
  assert.equal(canSeedAnswerEvent(stream, fresh, 'already rendered'), true)
})

test('an event that already has text keeps it', () => {
  const event = answer('mine')
  assert.equal(canSeedAnswerEvent([event], event, 'other text'), false)
})

test('nothing to inherit means nothing to seed', () => {
  const event = answer()
  for (const content of ['', '   ', undefined, null, 42, {}]) {
    assert.equal(canSeedAnswerEvent([event], event, content), false, `content=${JSON.stringify(content)}`)
  }
})
