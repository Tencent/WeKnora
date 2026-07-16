import assert from 'node:assert/strict'
import test from 'node:test'

import { getCredentialFieldRenderKind } from './datasourceFieldRendering.ts'

test('renders secret multiline fields as masked textarea instead of single-line password input', () => {
  assert.equal(getCredentialFieldRenderKind({ secret: true, multiline: true }), 'secret-textarea')
})

test('renders non-secret multiline fields as textarea', () => {
  assert.equal(getCredentialFieldRenderKind({ multiline: true }), 'textarea')
})
