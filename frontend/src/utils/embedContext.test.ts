import assert from 'node:assert/strict'
import test from 'node:test'

import { buildQueryWithHostContext, stripHostContextFromQuery } from './embedContext.ts'

test('strips injected host context from persisted user messages without changing the query', () => {
  const query = '我的userId是多少'
  const outboundQuery = buildQueryWithHostContext(query, { userId: '21', page: '/ai-agent' })

  assert.equal(
    outboundQuery,
    `[Host context]\nuserId: 21\npage: /ai-agent\n\n${query}`,
  )
  assert.equal(stripHostContextFromQuery(outboundQuery), query)
})

test('preserves ordinary user-message content without a host-context prefix', () => {
  assert.equal(stripHostContextFromQuery('我的userId是多少'), '我的userId是多少')
})

test('preserves malformed host-context content when its query separator is missing', () => {
  const malformed = '[Host context]\nuserId: 21'
  assert.equal(stripHostContextFromQuery(malformed), malformed)
})
