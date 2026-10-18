import assert from 'node:assert/strict'
import test from 'node:test'

import { VIDEO_LANGUAGE_VALUES, normalizeVideoLanguage } from './videoLanguage'

// ASR vendors (Whisper, Qwen ASR, …) accept only short codes such as zh / en,
// so the dropdown's values must stay codes. Storing a display name like
// "Chinese" would silently degrade recognition with no error anywhere.
test('preset language values stay vendor codes', () => {
  assert.deepEqual([...VIDEO_LANGUAGE_VALUES], ['zh', 'en', 'ja', 'ko', 'ru'])
  for (const value of VIDEO_LANGUAGE_VALUES) {
    assert.match(value, /^[a-z]{2,3}$/)
  }
})

test('stored preset codes round-trip unchanged', () => {
  for (const value of VIDEO_LANGUAGE_VALUES) {
    assert.equal(normalizeVideoLanguage(value), value)
  }
})

test('an empty or cleared select means auto-detect', () => {
  assert.equal(normalizeVideoLanguage(''), '')
  assert.equal(normalizeVideoLanguage('   '), '')
  assert.equal(normalizeVideoLanguage(undefined), '')
  assert.equal(normalizeVideoLanguage(null), '')
})

test('a custom code typed through creatable survives', () => {
  assert.equal(normalizeVideoLanguage('es'), 'es')
  assert.equal(normalizeVideoLanguage('fr'), 'fr')
  assert.equal(normalizeVideoLanguage('yue'), 'yue')
})

test('typed codes are trimmed before they reach the payload', () => {
  assert.equal(normalizeVideoLanguage(' zh '), 'zh')
  assert.equal(normalizeVideoLanguage('\ten\n'), 'en')
})

test('non-string input normalises to an empty (auto-detect) language', () => {
  assert.equal(normalizeVideoLanguage(42), '')
  assert.equal(normalizeVideoLanguage(['zh']), '')
  assert.equal(normalizeVideoLanguage({ language: 'zh' }), '')
})

test('the stored and submitted language is always a string', () => {
  for (const input of [undefined, null, 0, [], {}, 'zh', 'es']) {
    assert.equal(typeof normalizeVideoLanguage(input), 'string')
  }
})
