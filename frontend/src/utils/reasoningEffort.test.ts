import assert from 'node:assert/strict'
import test from 'node:test'

import {
  defaultReasoningEffort,
  resolveReasoningEffort,
} from './reasoningEffort.ts'

// Cases mirror internal/models/chat/responses.go resolveResponsesEffort.
test('reasoning effort defaults to medium and passes the v1 set through', () => {
  assert.equal(defaultReasoningEffort(), 'medium')
  assert.equal(resolveReasoningEffort(undefined), 'medium')
  assert.equal(resolveReasoningEffort(''), 'medium')
  assert.equal(resolveReasoningEffort('low'), 'low')
  assert.equal(resolveReasoningEffort('none'), 'none')
  assert.equal(resolveReasoningEffort(' High '), 'high')
  assert.equal(resolveReasoningEffort('ultra'), 'medium')
  assert.equal(resolveReasoningEffort('xhigh'), 'medium')
})
