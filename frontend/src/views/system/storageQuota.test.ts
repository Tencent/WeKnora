import assert from 'node:assert/strict'
import test from 'node:test'
import { formatStorageQuota, resolveStorageQuota } from './storageQuota.ts'

test('bulk confirmation uses the MB override even when invoked from the GB row', () => {
  assert.deepEqual(resolveStorageQuota([
    { key: 'tenant.default_storage_quota_gb', value: 10 },
    { key: 'tenant.default_storage_quota_mb', value: 50 },
  ]), { value: 50, unit: 'MB' })
  assert.deepEqual(formatStorageQuota(50 * 1024 ** 2), { value: 50, unit: 'MB' })
})

test('disabled or overflowing MB restores the legacy default shown in confirmation', () => {
  for (const value of [0, -1, 8796093022208]) {
    assert.deepEqual(resolveStorageQuota([
      { key: 'tenant.default_storage_quota_gb', value: 2 },
      { key: 'tenant.default_storage_quota_mb', value },
    ]), { value: 2, unit: 'GB' })
  }
  assert.deepEqual(resolveStorageQuota([]), { value: 10, unit: 'GB' })
  assert.deepEqual(resolveStorageQuota([{ key: 'tenant.default_storage_quota_gb', value: -1 }]),
    { value: 10, unit: 'GB' })
  assert.deepEqual(formatStorageQuota(2 * 1024 ** 3), { value: 2, unit: 'GB' })
})
