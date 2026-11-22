import assert from 'node:assert/strict'
import test from 'node:test'

import {
  PASSWORD_SPECIAL_CHAR_REGEX,
  PASSWORD_SPECIAL_CHARS,
  newPasswordRules,
} from './passwordPolicy.ts'

/** Stand-in for the i18n translate function: echo the key so assertions stay readable. */
function t(key: string, params?: Record<string, unknown>): string {
  return params ? `${key}:${JSON.stringify(params)}` : key
}

/** Collect the pattern rules of a rule set in order. */
function patternRules(rules: ReturnType<typeof newPasswordRules>) {
  // `pattern` is typed `string | RegExp`; narrow to RegExp so `.test()` and
  // `.source` type-check (all patterns produced here are regexes).
  return rules.filter(
    (rule): rule is typeof rule & { pattern: RegExp } =>
      rule.pattern instanceof RegExp,
  )
}

/** Return the messages of the pattern rules that the password fails. */
function failedPatternMessages(password: string, complexEnabled: boolean): string[] {
  return patternRules(newPasswordRules(t, complexEnabled))
    .filter((rule) => rule.pattern && !rule.pattern.test(password))
    .map((rule) => rule.message as string)
}

test('newPasswordRules returns the shared required and length rules in both modes', () => {
  for (const complexEnabled of [false, true]) {
    const [required, min, max] = newPasswordRules(t, complexEnabled)
    assert.equal(required.required, true)
    assert.equal(required.message, 'auth.passwordRequired')
    assert.equal(min.min, 8)
    assert.equal(min.message, 'auth.passwordMinLength')
    assert.equal(max.max, 32)
    assert.equal(max.message, 'auth.passwordMaxLength')
  }
})

test('newPasswordRules in simple mode requires a letter and a number only', () => {
  const rules = newPasswordRules(t, false)
  assert.equal(rules.length, 5)
  assert.deepEqual(
    patternRules(rules).map((rule) => rule.pattern?.source),
    [/[a-zA-Z]/, /\d/].map((pattern) => pattern.source),
  )
  assert.deepEqual(
    patternRules(rules).map((rule) => rule.message),
    ['auth.passwordMustContainLetter', 'auth.passwordMustContainNumber'],
  )
})

test('newPasswordRules in complex mode requires lowercase, uppercase, number and special char', () => {
  const rules = newPasswordRules(t, true)
  assert.equal(rules.length, 7)
  assert.deepEqual(
    patternRules(rules).map((rule) => rule.pattern?.source),
    [/[a-z]/, /[A-Z]/, /\d/, PASSWORD_SPECIAL_CHAR_REGEX].map((pattern) => pattern.source),
  )
  assert.deepEqual(
    patternRules(rules).map((rule) => rule.message),
    [
      'auth.passwordMustContainLowercaseLetter',
      'auth.passwordMustContainUppercaseLetter',
      'auth.passwordMustContainNumber',
      `auth.passwordMustContainSpecialChar:${JSON.stringify({ specialChars: PASSWORD_SPECIAL_CHARS })}`,
    ],
  )
})

test('newPasswordRules appends extra rules after the built-in rules in both modes', () => {
  const extra = [{ pattern: /z/, message: 'extra-rule', type: 'error' as const }]
  for (const complexEnabled of [false, true]) {
    const rules = newPasswordRules(t, complexEnabled, extra)
    assert.equal(rules.at(-1)?.message, 'extra-rule')
    assert.equal(rules.length, (complexEnabled ? 7 : 5) + 1)
  }
})

test('PASSWORD_SPECIAL_CHAR_REGEX matches every listed special character and nothing else', () => {
  for (const char of PASSWORD_SPECIAL_CHARS) {
    assert.ok(PASSWORD_SPECIAL_CHAR_REGEX.test(char), char)
  }
  for (const char of ['a', 'Z', '0', ' ', '~', '`', '/', '\\', '"', "'"]) {
    assert.equal(PASSWORD_SPECIAL_CHAR_REGEX.test(char), false, char)
  }
})

test('the complex rule set accepts a compliant password and rejects each missing class', () => {
  assert.deepEqual(failedPatternMessages('Abcdef1!', true), [])
  assert.deepEqual(failedPatternMessages('abcdef1!', true), [
    'auth.passwordMustContainUppercaseLetter',
  ])
  assert.deepEqual(failedPatternMessages('ABCDEF1!', true), [
    'auth.passwordMustContainLowercaseLetter',
  ])
  assert.deepEqual(failedPatternMessages('Abcdefg!', true), ['auth.passwordMustContainNumber'])
  assert.deepEqual(failedPatternMessages('Abcdef12', true), [
    `auth.passwordMustContainSpecialChar:${JSON.stringify({ specialChars: PASSWORD_SPECIAL_CHARS })}`,
  ])
})

test('the simple rule set accepts letters plus numbers and rejects either missing class', () => {
  assert.deepEqual(failedPatternMessages('abcdef12', false), [])
  assert.deepEqual(failedPatternMessages('12345678', false), ['auth.passwordMustContainLetter'])
  assert.deepEqual(failedPatternMessages('abcdefgh', false), ['auth.passwordMustContainNumber'])
})
