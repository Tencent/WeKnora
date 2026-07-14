import assert from 'node:assert/strict'
import test from 'node:test'

import { normalizeSessionTitleDraft } from './sessionTitleEdit.ts'

test('normalizeSessionTitleDraft trims whitespace and collapses inner runs', () => {
  assert.equal(normalizeSessionTitleDraft('  Quarterly   summary\t review  '), 'Quarterly summary review')
})

test('normalizeSessionTitleDraft rejects blank titles after trimming', () => {
  assert.equal(normalizeSessionTitleDraft(' \n\t '), '')
})

test('normalizeSessionTitleDraft caps long titles at 80 characters', () => {
  const input = 'x'.repeat(90)
  assert.equal(normalizeSessionTitleDraft(input), 'x'.repeat(80))
})
