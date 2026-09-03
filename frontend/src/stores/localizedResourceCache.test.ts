import assert from 'node:assert/strict'
import test from 'node:test'
import {
  isLocalizedCacheFresh,
  shouldCommitLocalizedGeneration,
  shouldForceLocalizedRefetch,
  shouldReuseLocalizedInflight,
} from './localizedResourceCache.ts'

const TTL_MS = 60_000

test('localized cache is fresh only when TTL and request locale both match', () => {
  const loadedAt = 1_000
  assert.equal(isLocalizedCacheFresh(loadedAt, 'zh-CN', 'zh-CN', TTL_MS, 2_000), true)
  assert.equal(isLocalizedCacheFresh(loadedAt, 'zh-CN', 'en-US', TTL_MS, 2_000), false)
  assert.equal(isLocalizedCacheFresh(loadedAt, 'zh-CN', 'zh-CN', TTL_MS, loadedAt + TTL_MS), false)
  assert.equal(isLocalizedCacheFresh(0, 'zh-CN', 'zh-CN', TTL_MS, 2_000), false)
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

test('stamping the request locale keeps a late zh-CN payload from looking fresh for en-US', () => {
  const requestLocale = 'zh-CN'
  const loadedAt = Date.now()
  // Wrong (old bug): stamp getCurrentLanguage() after await → 'en-US'.
  assert.equal(isLocalizedCacheFresh(loadedAt, 'en-US', 'en-US', TTL_MS), true)
  // Correct: stamp the locale the HTTP call was started with.
  assert.equal(isLocalizedCacheFresh(loadedAt, requestLocale, 'en-US', TTL_MS), false)
})

test('a superseded generation must not write the cache', () => {
  assert.equal(shouldCommitLocalizedGeneration(1, 1), true)
  assert.equal(shouldCommitLocalizedGeneration(1, 2), false)
})
