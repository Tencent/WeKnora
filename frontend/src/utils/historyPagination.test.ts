import assert from 'node:assert/strict'
import test from 'node:test'
import { historyHasMoreAfterAttempt } from './historyPagination'

test('a failed page request preserves the existing retry state', () => {
  assert.equal(historyHasMoreAfterAttempt(true, { status: 'failed' }), true)
  assert.equal(historyHasMoreAfterAttempt(false, { status: 'failed' }), false)
})

test('successful pages exhaust history only at a real pagination boundary', () => {
  assert.equal(
    historyHasMoreAfterAttempt(true, {
      status: 'loaded',
      itemCount: 20,
      pageSize: 20,
      cursorAdvanced: true,
    }),
    true,
  )
  assert.equal(
    historyHasMoreAfterAttempt(true, {
      status: 'loaded',
      itemCount: 19,
      pageSize: 20,
      cursorAdvanced: true,
    }),
    false,
  )
  assert.equal(
    historyHasMoreAfterAttempt(true, {
      status: 'loaded',
      itemCount: 20,
      pageSize: 20,
      cursorAdvanced: false,
    }),
    false,
  )
  assert.equal(
    historyHasMoreAfterAttempt(false, {
      status: 'loaded',
      itemCount: 20,
      pageSize: 20,
      cursorAdvanced: true,
    }),
    false,
  )
})
