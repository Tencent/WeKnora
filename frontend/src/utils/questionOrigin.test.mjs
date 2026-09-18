import assert from 'node:assert/strict'
import test from 'node:test'

import { originForSentText, questionOriginFromSuggestion } from './questionOrigin.ts'

test('questionOriginFromSuggestion keeps the knowledge source of a suggestion', () => {
  const pending = questionOriginFromSuggestion({
    question: '为什么在比较图形时需要谨慎处理？',
    knowledge_base_id: ' kb-1 ',
    knowledge_id: 'doc-1',
  })
  assert.deepEqual(pending, {
    question: '为什么在比较图形时需要谨慎处理？',
    origin: { knowledge_base_id: 'kb-1', knowledge_id: 'doc-1' },
  })
  assert.deepEqual(questionOriginFromSuggestion({ question: 'wiki', knowledge_base_id: 'kb-2' })?.origin, {
    knowledge_base_id: 'kb-2',
  })
})

test('questionOriginFromSuggestion ignores starters without a knowledge source', () => {
  assert.equal(questionOriginFromSuggestion({ question: '你好' }), null)
  assert.equal(questionOriginFromSuggestion(null), null)
})

test('originForSentText applies only to the picked question', () => {
  const pending = { question: 'Why?', origin: { knowledge_base_id: 'kb-1' } }
  assert.deepEqual(originForSentText(pending, ' Why? '), { knowledge_base_id: 'kb-1' })
  assert.equal(originForSentText(pending, 'Something else'), undefined)
  assert.equal(originForSentText(null, 'Why?'), undefined)
})
