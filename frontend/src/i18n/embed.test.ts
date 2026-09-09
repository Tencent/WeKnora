import assert from 'node:assert/strict'
import { test, type TestContext } from 'node:test'
import { applyEmbedLocale, EMBED_LOCALE_STORAGE_KEY, normalizeEmbedLocale, resolveInitialEmbedLocale, syncEmbedLocaleFromUrl } from './embed.ts'

function browser(t: TestContext, search = '', language = 'fr-CA') {
  const storage = new Map<string, string>([['locale', 'ja-JP']])
  const globals = {
    window: { location: { search } },
    navigator: { language },
    localStorage: {
      getItem: (key: string) => storage.get(key) ?? null,
      setItem: (key: string, value: string) => { storage.set(key, value) },
    },
  }
  for (const [key, value] of Object.entries(globals)) {
    const previous = Object.getOwnPropertyDescriptor(globalThis, key)
    Object.defineProperty(globalThis, key, { value, configurable: true })
    t.after(() => {
      if (previous) Object.defineProperty(globalThis, key, previous)
      else Reflect.deleteProperty(globalThis, key)
    })
  }
  return storage
}

test('French host and browser variants resolve to fr-FR', () => {
  for (const locale of ['fr', 'fr-FR', ' FR-fr ', 'fr-CA', 'fr_BE']) {
    assert.equal(normalizeEmbedLocale(locale), 'fr-FR', locale)
  }
  assert.equal(normalizeEmbedLocale('invalid'), 'zh-CN')
})

test('embed detects French browser language without using the main app preference', (t) => {
  browser(t)
  assert.equal(resolveInitialEmbedLocale(), 'fr-FR')
})

test('explicit embed URL locale takes precedence over its saved locale', (t) => {
  const storage = browser(t, '?locale=fr-FR', 'en-US')
  storage.set(EMBED_LOCALE_STORAGE_KEY, 'ko-KR')
  assert.equal(resolveInitialEmbedLocale(), 'fr-FR')
  const ref = { value: 'en-US' }
  assert.equal(syncEmbedLocaleFromUrl(ref), true)
  assert.equal(ref.value, 'fr-FR')
  assert.equal(storage.get(EMBED_LOCALE_STORAGE_KEY), 'fr-FR')
  assert.equal(storage.get('locale'), 'ja-JP')
})

test('saved embed preference survives a reload independently of the main app', (t) => {
  const storage = browser(t, '', 'en-US')
  const ref = { value: 'en-US' }
  applyEmbedLocale('fr', ref)
  assert.equal(resolveInitialEmbedLocale(), 'fr-FR')
  assert.equal(syncEmbedLocaleFromUrl(ref), false)
  assert.equal(storage.get('locale'), 'ja-JP')
})

test('French embed remains usable when localStorage is blocked', (t) => {
  browser(t)
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, get: () => { throw new Error('Storage blocked') } })
  assert.equal(resolveInitialEmbedLocale(), 'fr-FR')
  const ref = { value: 'en-US' }
  applyEmbedLocale('fr-FR', ref)
  assert.equal(ref.value, 'fr-FR')
})
