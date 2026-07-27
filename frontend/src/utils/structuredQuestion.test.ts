import assert from 'node:assert/strict'
import test from 'node:test'

import {
  buildStructuredAnswer,
  remainingQuestionCount,
  structuredQuestionProgress,
  type StructuredQuestionState,
} from './structuredQuestion.ts'

function state(overrides: Partial<StructuredQuestionState> = {}): StructuredQuestionState {
  return {
    mode: 'single_choice',
    selectedOptionIds: [],
    otherSelected: false,
    otherText: '',
	value: undefined,
    skipped: false,
    allowOther: true,
    allowSkip: true,
    ...overrides,
  }
}

test('remainingQuestionCount clamps invalid progress at zero', () => {
  assert.equal(remainingQuestionCount(1, 3), 2)
  assert.equal(remainingQuestionCount(3, 3), 0)
  assert.equal(remainingQuestionCount(4, 3), 0)
})

test('buildStructuredAnswer accepts one single-choice option', () => {
  assert.deepEqual(buildStructuredAnswer(state({ selectedOptionIds: ['written'] })), {
    selected_option_ids: ['written'],
    other_text: '',
    skipped: false,
  })
})

test('single choice treats Other as an exclusive option', () => {
  assert.deepEqual(buildStructuredAnswer(state({ otherSelected: true, otherText: '直接停用了账号' })), {
    selected_option_ids: [],
    other_text: '直接停用了账号',
    skipped: false,
  })
  assert.equal(buildStructuredAnswer(state({
    selectedOptionIds: ['written'], otherSelected: true, otherText: '其他',
  })), null)
})

test('multiple choice accepts predefined options plus Other', () => {
  assert.deepEqual(buildStructuredAnswer(state({
    mode: 'multiple_choice',
    selectedOptionIds: ['written', 'verbal'],
    otherSelected: true,
    otherText: '另有邮件',
  })), {
    selected_option_ids: ['written', 'verbal'],
    other_text: '另有邮件',
    skipped: false,
  })
})

test('Other requires nonblank text and permission', () => {
  assert.equal(buildStructuredAnswer(state({ otherSelected: true, otherText: '  ' })), null)
  assert.equal(buildStructuredAnswer(state({ allowOther: false, otherSelected: true, otherText: '其他' })), null)
})

test('skip produces an exclusive payload only when allowed', () => {
  assert.deepEqual(buildStructuredAnswer(state({ skipped: true })), {
    selected_option_ids: [],
    other_text: '',
    skipped: true,
  })
  assert.equal(buildStructuredAnswer(state({ skipped: true, allowSkip: false })), null)
  assert.equal(buildStructuredAnswer(state({ skipped: true, selectedOptionIds: ['written'] })), null)
})

test('empty and invalid choice counts cannot be submitted', () => {
  assert.equal(buildStructuredAnswer(state()), null)
  assert.equal(buildStructuredAnswer(state({ selectedOptionIds: ['written', 'verbal'] })), null)
})

test('typed answers trim text and enforce validation', () => {
  assert.deepEqual(buildStructuredAnswer(state({
    mode: 'short_text', fieldKey: 'reason', schemaVersion: 3, value: '  协商解除  ',
    validation: { max_length: 20 },
  })), {
    field_key: 'reason', schema_version: 3, selected_option_ids: [],
    value: '协商解除', other_text: '', skipped: false,
  })
  assert.equal(buildStructuredAnswer(state({ mode: 'short_text', value: '太长', validation: { max_length: 1 } })), null)
})

test('number and date answers must be finite and bounded', () => {
  assert.equal(buildStructuredAnswer(state({ mode: 'number', value: Infinity })), null)
  assert.equal(buildStructuredAnswer(state({ mode: 'number', value: 2, validation: { min_number: 3 } })), null)
  assert.equal(buildStructuredAnswer(state({ mode: 'date', value: '22/07/2026' })), null)
  assert.deepEqual(buildStructuredAnswer(state({ mode: 'date', value: '2026-07-22' })), {
    selected_option_ids: [], value: '2026-07-22', other_text: '', skipped: false,
  })
})

test('progress prefers dynamic server counts', () => {
  assert.deepEqual(structuredQuestionProgress({
    question_index: 2, question_total: 4, completed_count: 5, remaining_count: 1,
  }), { completed: 5, remaining: 1 })
  assert.deepEqual(structuredQuestionProgress({ question_index: 2, question_total: 4 }), { completed: 1, remaining: 3 })
})

test('answered or skipped terminal questions include the current question in progress', () => {
  const event = { question_index: 1, question_total: 1, completed_count: 0, remaining_count: 1 }
  assert.deepEqual(structuredQuestionProgress(event, true), { completed: 1, remaining: 0 })
  assert.deepEqual(structuredQuestionProgress(event, false), { completed: 0, remaining: 1 })
})
