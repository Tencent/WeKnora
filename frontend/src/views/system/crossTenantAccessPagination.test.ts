import assert from 'node:assert/strict'
import test from 'node:test'

import {
  CROSS_TENANT_ACCESS_PAGE_SIZE,
  fetchAllCrossTenantAccessUsers,
} from './crossTenantAccessPagination.ts'

test('loads every cross-tenant access page beyond the first 200 users', async () => {
  const source = Array.from({ length: 450 }, (_, index) => ({ id: String(index) }))
  const offsets: number[] = []

  const users = await fetchAllCrossTenantAccessUsers(async ({ offset, limit }) => {
    offsets.push(offset)
    assert.equal(limit, CROSS_TENANT_ACCESS_PAGE_SIZE)
    return {
      total: source.length,
      users: source.slice(offset, offset + limit),
    }
  })

  assert.deepEqual(offsets, [0, 200, 400])
  assert.deepEqual(users, source)
})

test('stops on an empty trailing page when the server total becomes stale', async () => {
  let calls = 0
  const users = await fetchAllCrossTenantAccessUsers(async () => {
    calls++
    return calls === 1
      ? { total: 2, users: [{ id: 'only-result' }] }
      : { total: 2, users: [] }
  })

  assert.equal(calls, 2)
  assert.deepEqual(users, [{ id: 'only-result' }])
})
