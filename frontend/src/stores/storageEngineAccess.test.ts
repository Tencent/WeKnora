import assert from 'node:assert/strict'
import test from 'node:test'

import { isStorageConfigDenied } from './storageEngineAccess.ts'

/** Mirrors withHttpStatus in utils/request.ts: rejected values carry a
 * non-enumerable `status` property on the payload. */
function rejectedWithStatus(status: number, message = 'forbidden'): unknown {
  const payload: Record<string, unknown> = { message }
  Object.defineProperty(payload, 'status', { value: status, enumerable: false })
  return payload
}

test('isStorageConfigDenied accepts permission rejections', () => {
  assert.equal(isStorageConfigDenied(rejectedWithStatus(403)), true)
  assert.equal(isStorageConfigDenied(rejectedWithStatus(401)), true)
})

test('isStorageConfigDenied rejects other failures so they stay fatal', () => {
  assert.equal(isStorageConfigDenied(rejectedWithStatus(500)), false)
  assert.equal(isStorageConfigDenied(rejectedWithStatus(404)), false)
  assert.equal(isStorageConfigDenied(new Error('network down')), false)
  assert.equal(isStorageConfigDenied(null), false)
  assert.equal(isStorageConfigDenied(undefined), false)
  assert.equal(isStorageConfigDenied('403'), false)
})
