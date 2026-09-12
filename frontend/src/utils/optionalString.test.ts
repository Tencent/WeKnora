import assert from 'node:assert/strict'
import test from 'node:test'

import { normalizeOptionalString } from './optionalString'

test('preserves a selected optional string', () => {
  assert.equal(normalizeOptionalString('kb-1'), 'kb-1')
})

test('serializes cleared optional strings as an explicit empty value', () => {
  assert.equal(normalizeOptionalString(undefined), '')
  assert.equal(normalizeOptionalString(null), '')
  assert.equal(
    JSON.stringify({ knowledge_base_id: normalizeOptionalString(undefined) }),
    '{"knowledge_base_id":""}',
  )
})
