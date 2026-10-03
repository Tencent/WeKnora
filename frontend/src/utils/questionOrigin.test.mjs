import assert from 'node:assert/strict'
import test from 'node:test'

import { questionOriginFromSuggestion } from './questionOrigin.ts'

test('questionOriginFromSuggestion keeps the knowledge source of a suggestion', () => {
  assert.deepEqual(
    questionOriginFromSuggestion({ knowledge_base_id: ' kb-1 ', knowledge_id: ' doc-1 ' }),
    { knowledge_base_id: 'kb-1', knowledge_id: 'doc-1' },
  )
  assert.deepEqual(questionOriginFromSuggestion({ knowledge_base_id: 'kb-2' }), { knowledge_base_id: 'kb-2' })
})

test('questionOriginFromSuggestion ignores starters without a knowledge source', () => {
  assert.equal(questionOriginFromSuggestion({}), undefined)
  assert.equal(questionOriginFromSuggestion({ knowledge_base_id: '  ' }), undefined)
  assert.equal(questionOriginFromSuggestion(null), undefined)
})
