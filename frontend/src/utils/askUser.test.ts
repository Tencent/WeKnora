import assert from 'node:assert/strict'
import test from 'node:test'
import { buildAskUserResponse, canSubmitAskUser, summarizeAskUserAnswer, type AskUserQuestion } from './askUser.ts'

const form: AskUserQuestion = {
  question: 'Trip?',
  fields: [
    { name: 'city', label: 'City', type: 'text', required: true },
    { name: 'days', type: 'number' },
    { name: 'tags', type: 'multiselect', options: ['a', 'b'] },
  ],
}

test('a form answer keeps filled fields only', () => {
  const r = buildAskUserResponse(form, { selected: [], values: { city: ' Paris ', days: 3, tags: [] }, text: 'ignored' })
  assert.deepEqual(r, { values: { city: 'Paris', days: 3 } })
  assert.equal(canSubmitAskUser(form, r), true)
  assert.equal(summarizeAskUserAnswer(form, r), 'City: Paris\ndays: 3')
})

test('a form waits for its required fields', () => {
  const r = buildAskUserResponse(form, { selected: [], values: { days: 2 }, text: '' })
  assert.equal(canSubmitAskUser(form, r), false)
})

test('options accept a choice or, when allowed, typed text', () => {
  const q: AskUserQuestion = { question: 'Pick', options: ['x', 'y'], allow_free_text: true }
  assert.equal(canSubmitAskUser(q, buildAskUserResponse(q, { selected: [], values: {}, text: '' })), false)
  assert.deepEqual(buildAskUserResponse(q, { selected: ['y'], values: {}, text: '' }), { selected: ['y'] })
  const typed = buildAskUserResponse(q, { selected: [], values: {}, text: 'z' })
  assert.deepEqual(typed, { text: 'z' })
  assert.equal(canSubmitAskUser(q, typed), true)
  const strict: AskUserQuestion = { question: 'Pick', options: ['x'] }
  assert.deepEqual(buildAskUserResponse(strict, { selected: [], values: {}, text: 'z' }), {})
})

test('a free-text question needs text', () => {
  const q: AskUserQuestion = { question: 'Why?' }
  assert.equal(canSubmitAskUser(q, buildAskUserResponse(q, { selected: [], values: {}, text: '  ' })), false)
  assert.deepEqual(buildAskUserResponse(q, { selected: [], values: {}, text: ' because ' }), { text: 'because' })
})
