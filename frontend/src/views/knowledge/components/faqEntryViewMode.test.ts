import assert from 'node:assert/strict'
import test from 'node:test'

import { parseFAQEntryViewMode } from './faqEntryViewMode.ts'

test('restores the FAQ list view only for its explicit persisted value', () => {
  assert.equal(parseFAQEntryViewMode('list'), 'list')
  assert.equal(parseFAQEntryViewMode('card'), 'card')
  assert.equal(parseFAQEntryViewMode('grid'), 'card')
  assert.equal(parseFAQEntryViewMode(null), 'card')
})
