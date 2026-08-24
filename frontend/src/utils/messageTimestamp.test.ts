import assert from 'node:assert/strict'
import test from 'node:test'
import {
  applyMessageCreatedAt,
  bindServerTurnTimestamps,
  ensureMessageCreatedAt,
  formatMessageTimestamp,
  normalizeMessageCreatedAt,
} from './messageTimestamp'

test('normalizeMessageCreatedAt accepts parseable timestamps and rejects junk', () => {
  assert.equal(normalizeMessageCreatedAt('2026-01-02T03:04:05.000Z'), '2026-01-02T03:04:05.000Z')
  assert.equal(normalizeMessageCreatedAt('  2026-01-02T03:04:05.000Z  '), '2026-01-02T03:04:05.000Z')
  assert.equal(normalizeMessageCreatedAt(''), '')
  assert.equal(normalizeMessageCreatedAt('   '), '')
  assert.equal(normalizeMessageCreatedAt('not-a-date'), '')
  assert.equal(normalizeMessageCreatedAt(undefined), '')
  assert.equal(normalizeMessageCreatedAt(1735787045000), '')
})

test('ensureMessageCreatedAt fills a missing timestamp with the provided fallback', () => {
  const message: Record<string, unknown> = { role: 'assistant' }
  const result = ensureMessageCreatedAt(message, '2026-01-02T03:04:05.000Z')

  assert.equal(result, message)
  assert.equal(message.created_at, '2026-01-02T03:04:05.000Z')
})

test('ensureMessageCreatedAt replaces empty and invalid local timestamps', () => {
  const empty = { created_at: '' }
  const blank = { created_at: '   ' }
  const invalid = { created_at: 'not-a-date' }

  ensureMessageCreatedAt(empty, '2026-01-02T03:04:05.000Z')
  ensureMessageCreatedAt(blank, '2026-01-02T03:04:05.000Z')
  ensureMessageCreatedAt(invalid, '2026-01-02T03:04:05.000Z')

  assert.equal(empty.created_at, '2026-01-02T03:04:05.000Z')
  assert.equal(blank.created_at, '2026-01-02T03:04:05.000Z')
  assert.equal(invalid.created_at, '2026-01-02T03:04:05.000Z')
})

test('ensureMessageCreatedAt preserves a server timestamp', () => {
  const message = { created_at: '2026-02-03T04:05:06.000Z' }

  ensureMessageCreatedAt(message, '2026-01-02T03:04:05.000Z')

  assert.equal(message.created_at, '2026-02-03T04:05:06.000Z')
})

test('applyMessageCreatedAt overwrites a local fallback with a server timestamp', () => {
  const message = { created_at: '2026-01-02T03:04:05.000Z' }

  applyMessageCreatedAt(message, '2026-02-03T04:05:06.000Z')

  assert.equal(message.created_at, '2026-02-03T04:05:06.000Z')
})

test('applyMessageCreatedAt ignores empty or invalid server candidates', () => {
  const message = { created_at: '2026-01-02T03:04:05.000Z' }

  applyMessageCreatedAt(message, '')
  applyMessageCreatedAt(message, '   ')
  applyMessageCreatedAt(message, 'not-a-date')
  applyMessageCreatedAt(message, undefined)

  assert.equal(message.created_at, '2026-01-02T03:04:05.000Z')
})

test('formatMessageTimestamp renders local time to minute precision', () => {
  const localTime = new Date(2026, 0, 2, 3, 4, 5)

  assert.equal(formatMessageTimestamp(localTime.toISOString()), '2026-01-02 03:04')
})

test('formatMessageTimestamp converts a UTC string into local wall time', () => {
  const utc = '2026-01-02T00:00:00.000Z'
  const local = new Date(utc)
  const pad = (part: number) => String(part).padStart(2, '0')
  const expected = `${local.getFullYear()}-${pad(local.getMonth() + 1)}-${pad(local.getDate())} ${pad(local.getHours())}:${pad(local.getMinutes())}`

  assert.equal(formatMessageTimestamp(utc), expected)
  assert.notEqual(expected, '')
})

test('bindServerTurnTimestamps replaces local fallbacks with persisted server times', () => {
  const user: { role: string; created_at: string; id?: string } = {
    role: 'user',
    created_at: '2026-01-02T03:04:05.000Z',
  }
  const assistant = { role: 'assistant', created_at: '2026-01-02T03:04:06.000Z' }

  bindServerTurnTimestamps(
    [user, assistant],
    {
      user_message_id: 'user-1',
      user_created_at: '2026-01-02T03:05:00.000Z',
      assistant_created_at: '2026-01-02T03:05:01.000Z',
    },
    assistant,
  )

  assert.equal(user.id, 'user-1')
  assert.equal(user.created_at, '2026-01-02T03:05:00.000Z')
  assert.equal(assistant.created_at, '2026-01-02T03:05:01.000Z')
})

test('bindServerTurnTimestamps ignores junk and keeps existing user ids', () => {
  const user = { id: 'existing-user', role: 'user', created_at: '2026-01-02T03:04:05.000Z' }
  const assistant = { role: 'assistant', created_at: '2026-01-02T03:04:06.000Z' }

  bindServerTurnTimestamps(
    [user, assistant],
    {
      user_message_id: 'user-1',
      user_created_at: 'not-a-date',
      assistant_created_at: '',
    },
    assistant,
  )

  assert.equal(user.id, 'existing-user')
  assert.equal(user.created_at, '2026-01-02T03:04:05.000Z')
  assert.equal(assistant.created_at, '2026-01-02T03:04:06.000Z')
})

test('formatMessageTimestamp hides empty and invalid timestamps', () => {
  assert.equal(formatMessageTimestamp(''), '')
  assert.equal(formatMessageTimestamp('   '), '')
  assert.equal(formatMessageTimestamp('not-a-date'), '')
  assert.equal(formatMessageTimestamp(undefined), '')
})
