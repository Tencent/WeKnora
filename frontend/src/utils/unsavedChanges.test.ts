import assert from 'node:assert/strict'
import test from 'node:test'
import { hasUnsavedChanges, registerUnsavedChanges } from './unsavedChanges'

test('any registered editor with changes counts until it unregisters', () => {
  let dirty = false
  const clean = registerUnsavedChanges(() => false)
  const editor = registerUnsavedChanges(() => dirty)
  const broken = registerUnsavedChanges(() => { throw new Error('torn down') })
  assert.equal(hasUnsavedChanges(), false)
  dirty = true
  assert.equal(hasUnsavedChanges(), true)
  editor()
  assert.equal(hasUnsavedChanges(), false, 'an unregistered editor no longer counts')
  clean()
  broken()
})
