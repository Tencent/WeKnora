import assert from 'node:assert/strict'
import test from 'node:test'

import {
  ASR_LANGUAGE_AUTO,
  ASR_LANGUAGE_VALUES,
  fromAsrLanguageModel,
  normalizeAsrLanguage,
  toAsrLanguageModel,
} from './asrLanguage'

// ASR 厂商（whisper 等）只认 zh / en / ja … 这类短代码，所以下拉的选项值必须是
// 代码本身；把它改成 Chinese / English 会把用户的转写语言搜索悄悄弄坏。
test('preset language values stay vendor codes', () => {
  assert.deepEqual([...ASR_LANGUAGE_VALUES], ['zh', 'en', 'ja', 'ko', 'ru'])
  for (const value of ASR_LANGUAGE_VALUES) {
    assert.match(value, /^[a-z]{2,3}$/)
  }
})

test('stored codes map straight onto the select model', () => {
  assert.equal(toAsrLanguageModel('zh'), 'zh')
  assert.equal(toAsrLanguageModel('en'), 'en')
  assert.equal(toAsrLanguageModel('ch'), 'ch', 'a legacy code from a free-text input still renders')
})

test('an empty stored language shows the explicit auto-detect option', () => {
  assert.equal(toAsrLanguageModel(''), ASR_LANGUAGE_AUTO)
  assert.equal(toAsrLanguageModel(undefined), ASR_LANGUAGE_AUTO)
  assert.equal(toAsrLanguageModel(null), ASR_LANGUAGE_AUTO)
  assert.equal(toAsrLanguageModel('   '), ASR_LANGUAGE_AUTO)
})

test('picking auto-detect or clearing the select stores an empty language', () => {
  assert.equal(fromAsrLanguageModel(ASR_LANGUAGE_AUTO), '')
  assert.equal(fromAsrLanguageModel(undefined), '')
  assert.equal(fromAsrLanguageModel(null), '')
  assert.equal(fromAsrLanguageModel(''), '')
})

test('creatable custom codes survive untouched', () => {
  assert.equal(fromAsrLanguageModel('es'), 'es')
  assert.equal(fromAsrLanguageModel('fr'), 'fr')
  assert.equal(fromAsrLanguageModel('yue'), 'yue')
  assert.equal(toAsrLanguageModel('es'), 'es', 'a custom code stays selected after reopening')
})

test('typed codes are trimmed before they reach the payload', () => {
  assert.equal(fromAsrLanguageModel(' zh '), 'zh')
  assert.equal(normalizeAsrLanguage('\ten\n'), 'en')
})

test('non-string input normalises to an empty (auto-detect) language', () => {
  assert.equal(normalizeAsrLanguage(42), '')
  assert.equal(normalizeAsrLanguage(['zh']), '')
  assert.equal(normalizeAsrLanguage({ language: 'zh' }), '')
})

test('model round-trips every preset value', () => {
  for (const value of ASR_LANGUAGE_VALUES) {
    assert.equal(fromAsrLanguageModel(toAsrLanguageModel(value)), value)
  }
  assert.equal(fromAsrLanguageModel(toAsrLanguageModel('')), '')
})
