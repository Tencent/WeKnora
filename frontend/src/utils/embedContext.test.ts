import assert from 'node:assert/strict'
import test from 'node:test'
import { buildQueryWithHostContext, stripHostContextEnvelope } from './embedContext.ts'

test('build prefixes the host context as an envelope', () => {
  assert.equal(
    buildQueryWithHostContext('who am i', { userId: 21, page: '/ai-agent' }),
    '[Host context]\nuserId: 21\npage: /ai-agent\n\nwho am i',
  )
})

test('build leaves the query alone without usable context', () => {
  assert.equal(buildQueryWithHostContext('hello'), 'hello')
  assert.equal(buildQueryWithHostContext('hello', {}), 'hello')
  assert.equal(buildQueryWithHostContext('hello', { page: '' }), 'hello')
})

test('strip unwraps a single-field envelope', () => {
  assert.equal(
    stripHostContextEnvelope('[Host context]\nuserId: 21\n\nwho am i'),
    'who am i',
  )
})

test('strip unwraps a multi-field envelope', () => {
  assert.equal(
    stripHostContextEnvelope('[Host context]\nuserId: 21\npage: /ai-agent\n\nwho am i'),
    'who am i',
  )
})

test('strip closes the envelope at the first blank line', () => {
  // Context values may carry newlines; only the blank line ends the envelope.
  assert.equal(
    stripHostContextEnvelope('[Host context]\nnote: line1\nline2\n\nask'),
    'ask',
  )
})

test('strip keeps a multi-line question intact', () => {
  assert.equal(
    stripHostContextEnvelope('[Host context]\npage: /pricing\n\nfirst\n\nsecond'),
    'first\n\nsecond',
  )
})

test('strip leaves text without an envelope untouched', () => {
  assert.equal(stripHostContextEnvelope('who am i'), 'who am i')
  assert.equal(stripHostContextEnvelope(''), '')
  // The marker only counts at the very start of the message.
  assert.equal(stripHostContextEnvelope('quote: [Host context]\n\nx'), 'quote: [Host context]\n\nx')
})

test('strip leaves a bare marker untouched', () => {
  // No blank line means there is no body to recover.
  assert.equal(stripHostContextEnvelope('[Host context]\nuserId: 21'), '[Host context]\nuserId: 21')
})

test('strip recovers an empty question after the envelope', () => {
  assert.equal(stripHostContextEnvelope('[Host context]\nuserId: 21\n\n'), '')
})

test('strip inverts build', () => {
  const question = '我的 userId 是多少'
  const composed = buildQueryWithHostContext(question, { userId: '21', page: '/ai-agent' })
  assert.equal(stripHostContextEnvelope(composed), question)
})
