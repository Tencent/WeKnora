import assert from 'node:assert/strict'
import { test } from 'node:test'
import { compareLocaleMessages } from './localeChanges.ts'

test('translation review detects changed English wording even when the key is unchanged', () => {
  const before = { save: 'Save', removed: 'Old', nested: { 'flat.action': 'Before' }, items: ['First'] }
  const after = { save: 'Save changes', added: 'New', nested: { 'flat.action': 'After' }, items: ['First'] }
  assert.deepEqual(compareLocaleMessages(before, after), {
    added: ['added'], removed: ['removed'], changed: ['nested.flat.action', 'save'],
  })
  assert.deepEqual(compareLocaleMessages(before, before), { added: [], removed: [], changed: [] })
})
