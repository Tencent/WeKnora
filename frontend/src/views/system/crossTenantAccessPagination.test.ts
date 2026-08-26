import assert from 'node:assert/strict'
import test from 'node:test'

import {
  CROSS_TENANT_ACCESS_PAGE_SIZE,
  fetchAllCrossTenantAccessUsers,
  type CrossTenantAccessPage,
} from './crossTenantAccessPagination.ts'

test('loads every cross-tenant access page beyond the first 200 users', async () => {
  const source = Array.from({ length: 450 }, (_, index) => ({ id: String(index) }))
  const cursors: Array<string | undefined> = []

  const users = await fetchAllCrossTenantAccessUsers(async ({ cursor, limit }) => {
    cursors.push(cursor)
    assert.equal(limit, CROSS_TENANT_ACCESS_PAGE_SIZE)
    const offset = cursor ? Number(cursor) : 0
    const nextOffset = offset + limit
    return {
      users: source.slice(offset, offset + limit),
      next_cursor: nextOffset < source.length ? String(nextOffset) : '',
    }
  })

  assert.deepEqual(cursors, [undefined, '200', '400'])
  assert.deepEqual(users, source)
})

test('follows the server cursor when an already-read head account is revoked', async () => {
  const pages = new Map<string | undefined, CrossTenantAccessPage<{ id: string }>>([
    [undefined, { users: [{ id: 'a' }, { id: 'b' }], next_cursor: 'after-b' }],
    ['after-b', { users: [{ id: 'c' }], next_cursor: '' }],
  ])
  const users = await fetchAllCrossTenantAccessUsers(async ({ cursor }) => {
    const page = pages.get(cursor)
    assert.ok(page)
    return page
  })

  assert.deepEqual(users, [{ id: 'a' }, { id: 'b' }, { id: 'c' }])
})
