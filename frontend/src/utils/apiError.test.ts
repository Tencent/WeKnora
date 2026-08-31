import assert from 'node:assert/strict'
import test from 'node:test'
import { getApiErrorMessage } from './apiError'

test('prefers backend error message and appends its details', () => {
  assert.equal(
    getApiErrorMessage({ error: { message: 'parse failed', details: 'unsupported encoding' } }),
    'parse failed: unsupported encoding',
  )
})

test('supports axios response envelopes and object details', () => {
  assert.equal(
    getApiErrorMessage({
      response: { data: { error: { code: 'bad_file', details: { line: 3 } } } },
    }),
    '{"line":3}',
  )
})

test('falls back to top-level message and explicit fallback', () => {
  assert.equal(getApiErrorMessage({ message: 'network failed' }), 'network failed')
  assert.equal(getApiErrorMessage({}, 'upload failed'), 'upload failed')
})
