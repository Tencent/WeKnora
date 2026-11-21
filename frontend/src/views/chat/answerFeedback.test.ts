import test from 'node:test'
import assert from 'node:assert/strict'
import { feedbackIntent, normalizeFeedbackRating } from './answerFeedback.ts'

test('like toggles on and off', () => {
  assert.deepEqual(feedbackIntent('none', 'like'), { type: 'submit', rating: 'like' })
  assert.deepEqual(feedbackIntent('like', 'like'), { type: 'submit', rating: 'none' })
  assert.deepEqual(feedbackIntent('dislike', 'like'), { type: 'submit', rating: 'like' })
})

test('dislike prompts unless it is already selected', () => {
  assert.deepEqual(feedbackIntent('none', 'dislike'), { type: 'prompt' })
  assert.deepEqual(feedbackIntent('like', 'dislike'), { type: 'prompt' })
  assert.deepEqual(feedbackIntent('dislike', 'dislike'), { type: 'submit', rating: 'none' })
})

test('unknown stored ratings read as none', () => {
  assert.equal(normalizeFeedbackRating(undefined), 'none')
  assert.equal(normalizeFeedbackRating(''), 'none')
  assert.equal(normalizeFeedbackRating('like'), 'like')
})
