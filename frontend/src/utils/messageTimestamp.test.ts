import assert from 'node:assert/strict'
import test from 'node:test'
import { ensureMessageCreatedAt, formatMessageTimestamp } from './messageTimestamp'

test('ensureMessageCreatedAt fills a missing timestamp with the provided fallback', () => {
  const message: Record<string, unknown> = { role: 'assistant' }
  const result = ensureMessageCreatedAt(message, '2026-01-02T03:04:05.000Z')

  assert.equal(result, message)
  assert.equal(message.created_at, '2026-01-02T03:04:05.000Z')
})

test('ensureMessageCreatedAt preserves a server timestamp', () => {
  const message = { created_at: '2026-02-03T04:05:06.000Z' }

  ensureMessageCreatedAt(message, '2026-01-02T03:04:05.000Z')

  assert.equal(message.created_at, '2026-02-03T04:05:06.000Z')
})

test('formatMessageTimestamp renders local time to minute precision', () => {
  const localTime = new Date(2026, 0, 2, 3, 4, 5)

  assert.equal(formatMessageTimestamp(localTime.toISOString()), '2026-01-02 03:04')
})

test('formatMessageTimestamp hides empty and invalid timestamps', () => {
  assert.equal(formatMessageTimestamp(''), '')
  assert.equal(formatMessageTimestamp('not-a-date'), '')
  assert.equal(formatMessageTimestamp(undefined), '')
})
