import assert from 'node:assert/strict'
import test from 'node:test'

import { buildEmbedChatPayload } from './embedContext.ts'

test('buildEmbedChatPayload keeps host context out of the retrieval query', () => {
  const query = 'How do I reset my password?'
  const payload = buildEmbedChatPayload(query, {
    page: 'checkout',
    error: 'ERR_500',
  })

  assert.equal(payload.query, query)
  assert.ok(payload.prompt_context)
  assert.doesNotMatch(payload.query, /checkout|ERR_500|Host context/)
})

test('buildEmbedChatPayload serializes JSON, newlines, and special characters', () => {
  const hostContext = {
    details: {
      message: 'line one\nline two',
      symbols: '<>&"\'\\',
      values: [1, true, null],
    },
  }

  const payload = buildEmbedChatPayload('original question', hostContext)

  assert.equal(payload.query, 'original question')
  assert.ok(payload.prompt_context?.startsWith('[Host context]\n'))
  assert.deepEqual(
    JSON.parse(payload.prompt_context!.slice('[Host context]\n'.length)),
    hostContext,
  )
})

test('buildEmbedChatPayload is backward compatible without usable host context', () => {
  assert.deepEqual(buildEmbedChatPayload('hello'), { query: 'hello' })
  assert.deepEqual(buildEmbedChatPayload('hello', {}), { query: 'hello' })
  assert.deepEqual(
    buildEmbedChatPayload('hello', { empty: '', nil: null, missing: undefined }),
    { query: 'hello' },
  )
})
