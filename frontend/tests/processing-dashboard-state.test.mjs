import assert from 'node:assert/strict'
import test from 'node:test'
import { shouldShowRetryingCount } from '../src/views/processingDashboard/format.ts'

test('retrying column is unavailable when queue snapshot cannot observe retries', () => {
  assert.equal(shouldShowRetryingCount(false), false)
  assert.equal(shouldShowRetryingCount(true), true)
})
