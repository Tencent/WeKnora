import assert from 'node:assert/strict'
import test from 'node:test'
import {
  isLocalizedSnapshotUsable,
  shouldForceLocalizedRefetch,
  shouldReuseLocalizedInflight,
} from './localizedResourceCache.ts'

test('localized snapshot is usable only for the locale it was fetched in', () => {
  assert.equal(isLocalizedSnapshotUsable(true, 'zh-CN', 'zh-CN'), true)
  assert.equal(isLocalizedSnapshotUsable(true, 'zh-CN', 'en-US'), false)
  assert.equal(isLocalizedSnapshotUsable(false, 'zh-CN', 'zh-CN'), false)
  assert.equal(isLocalizedSnapshotUsable(true, '', 'zh-CN'), false)
})

test('landing prefetch must not be reused after the UI language changes', () => {
  // Request started as zh-CN; user switched to en-US before it settled.
  assert.equal(shouldReuseLocalizedInflight(true, 'zh-CN', 'en-US'), false)
  assert.equal(shouldForceLocalizedRefetch(true, 'zh-CN', 'en-US'), true)

  assert.equal(shouldReuseLocalizedInflight(true, 'en-US', 'en-US'), true)
  assert.equal(shouldForceLocalizedRefetch(true, 'en-US', 'en-US'), false)
  assert.equal(shouldReuseLocalizedInflight(false, 'zh-CN', 'zh-CN'), false)
  assert.equal(shouldReuseLocalizedInflight(true, '', 'en-US'), false)
})

test('stamping the request locale keeps a late zh-CN payload from looking usable for en-US', () => {
  const requestLocale = 'zh-CN'
  // Wrong (old bug): stamp getCurrentLanguage() after await → 'en-US'.
  assert.equal(isLocalizedSnapshotUsable(true, 'en-US', 'en-US'), true)
  // Correct: stamp the locale the HTTP call was started with.
  assert.equal(isLocalizedSnapshotUsable(true, requestLocale, 'en-US'), false)
})
