import assert from 'node:assert/strict'
import test from 'node:test'
import {
  allowsSave, buildSecureFormSubmission, canSubmitSecureForm, editableFields, fillDomains, formParts,
  originWarning, requestDomains, type CredentialVaultItem, type SecureFormRequest,
} from './secureForm.ts'

const login: SecureFormRequest = {
  title: '登录', kind: 'login', site: 'accounts.google.com', domains: ['accounts.google.com', 'sso.google.com'],
  observed_origin: 'https://accounts.google.com',
  extra: [{ name: 'otp', type: 'one_time_code', required: false }],
}

test('login parts default to username and password', () => {
  assert.deepEqual(formParts(login), ['username', 'password'])
  assert.deepEqual(formParts({ ...login, parts: ['password'] }), ['password'])
  assert.deepEqual(formParts({ title: 'x', site: 'a.com', extra: [] }), [])
  assert.equal(allowsSave(login), true)
  assert.equal(allowsSave({ ...login, allow_save: false }), false)
  assert.equal(allowsSave({ ...login, parts: ['username'] }), false)
})

test('domains shown and used for a saved login are the intersection', () => {
  assert.deepEqual(requestDomains(login), ['accounts.google.com', 'sso.google.com'])
  const item: CredentialVaultItem = { id: 'i1', title: 'g', site: 'accounts.google.com', domains: ['accounts.google.com'], username: 'a' }
  assert.deepEqual(fillDomains(login, item), ['accounts.google.com'])
  assert.deepEqual(fillDomains(login), ['accounts.google.com', 'sso.google.com'])
})

test('origin warnings', () => {
  assert.equal(originWarning(login), null)
  assert.equal(originWarning({ ...login, observed_origin: '' }), 'unknown')
  assert.equal(originWarning({ ...login, observed_origin: 'https://evil.com' }), 'mismatch')
  assert.equal(originWarning({ ...login, observed_origin: 'http://accounts.google.com' }), 'insecure')
})

test('a saved login only takes extra fields', () => {
  assert.deepEqual(editableFields(login, 'saved'), ['otp'])
  const s = buildSecureFormSubmission(login, { choice: 'saved', vaultItemId: 'i1', values: { password: 'p', otp: '12' } })
  assert.deepEqual(s, { action: 'submit', choice: 'saved', vault_item_id: 'i1', values: { otp: '12' } })
  assert.equal(canSubmitSecureForm(login, s), true)
  assert.equal(canSubmitSecureForm(login, { ...s, vault_item_id: undefined }), false)
})

test('new credentials need every login part; username is trimmed, password is not', () => {
  const s = buildSecureFormSubmission(login, { choice: 'use_once', values: { username: ' a ', password: ' p ' } })
  assert.deepEqual(s.values, { username: 'a', password: ' p ' })
  assert.equal(canSubmitSecureForm(login, s), true)
  const missing = buildSecureFormSubmission(login, { choice: 'save', values: { username: 'a' } })
  assert.equal(canSubmitSecureForm(login, missing), false)
  const noSave = buildSecureFormSubmission({ ...login, allow_save: false }, { choice: 'save', values: { username: 'a', password: 'p' } })
  assert.equal(canSubmitSecureForm({ ...login, allow_save: false }, noSave), false)
})
