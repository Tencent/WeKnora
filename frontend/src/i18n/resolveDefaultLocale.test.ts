import assert from 'node:assert/strict'
import { test } from 'node:test'

import { BUILT_IN_DEFAULT, resolveDefaultLocale } from './resolveDefaultLocale.ts'

test('resolveDefaultLocale prefers runtime over build-time default', () => {
  assert.equal(resolveDefaultLocale('en-US', 'ru-RU'), 'en-US')
})

test('resolveDefaultLocale falls back to build-time then built-in default', () => {
  assert.equal(resolveDefaultLocale('', 'ko-KR'), 'ko-KR')
  assert.equal(resolveDefaultLocale('', 'ja-JP'), 'ja-JP')
  assert.equal(resolveDefaultLocale(undefined, undefined), BUILT_IN_DEFAULT)
})

test('resolveDefaultLocale trims whitespace around supported tags', () => {
  assert.equal(resolveDefaultLocale(' en-US '), 'en-US')
})

test('resolveDefaultLocale rejects unknown values', () => {
  assert.equal(resolveDefaultLocale('es-ES'), BUILT_IN_DEFAULT)
  assert.equal(resolveDefaultLocale('en-US"};alert(1);//'), BUILT_IN_DEFAULT)
  assert.equal(resolveDefaultLocale('   '), BUILT_IN_DEFAULT)
})

test('French can be selected as the runtime or build-time default', () => {
  assert.equal(resolveDefaultLocale('fr-FR', 'en-US'), 'fr-FR')
  assert.equal(resolveDefaultLocale(undefined, 'fr-FR'), 'fr-FR')
  assert.equal(resolveDefaultLocale(' fr-FR '), 'fr-FR')
})
