import assert from 'node:assert/strict'
import test from 'node:test'

import { canAccessChunkFeedbackSettings } from './knowledgeBaseFeedbackAccess.ts'

test('an admin can access feedback settings for current-tenant document and FAQ knowledge bases', () => {
  assert.equal(canAccessChunkFeedbackSettings('edit', 'document-kb', true, true), true)
  assert.equal(canAccessChunkFeedbackSettings('edit', 'faq-kb', true, true), true)
})

test('feedback settings stay hidden while creating, cross-tenant, or without admin access', () => {
  assert.equal(canAccessChunkFeedbackSettings('create', 'faq-kb', true, true), false)
  assert.equal(canAccessChunkFeedbackSettings('edit', undefined, true, true), false)
  assert.equal(canAccessChunkFeedbackSettings('edit', 'faq-kb', false, true), false)
  assert.equal(canAccessChunkFeedbackSettings('edit', 'shared-faq-kb', true, false), false)
})
