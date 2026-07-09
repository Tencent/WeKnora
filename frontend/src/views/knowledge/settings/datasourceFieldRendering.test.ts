import assert from 'node:assert/strict'
import test from 'node:test'

import { getCredentialFieldRenderKind } from './datasourceFieldRendering.ts'

test('renders secret multiline fields as password input instead of plaintext textarea', () => {
  assert.equal(getCredentialFieldRenderKind({ secret: true, multiline: true }), 'password')
})

test('renders non-secret multiline fields as textarea', () => {
  assert.equal(getCredentialFieldRenderKind({ multiline: true }), 'textarea')
})
