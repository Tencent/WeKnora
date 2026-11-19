import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

const faqManager = readFileSync(new URL('./FAQEntryManager.vue', import.meta.url), 'utf8')

test('FAQEntryManager implements request sequence guard against stale paged responses', () => {
  // Verifies that a sequence counter is declared and incremented on load
  assert.match(faqManager, /let\s+loadRequestSeq\s*=\s*0/)
  assert.match(faqManager, /const\s+seq\s*=\s*\+\+loadRequestSeq/)

  // Verifies that stale responses are rejected after fetch calls
  assert.match(faqManager, /if\s*\(seq\s*!==\s*loadRequestSeq\)\s*return/)

  // Verifies that finally block only resets loading flags for the latest request
  assert.match(faqManager, /finally\s*\{\s*if\s*\(seq\s*===\s*loadRequestSeq\)/)
})
