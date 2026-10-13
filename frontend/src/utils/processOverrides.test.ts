import assert from 'node:assert/strict'
import test from 'node:test'

import { diffProcessOverrides } from './processOverrides.ts'

test('diffProcessOverrides drops fields that still match the knowledge base', () => {
  const baseline = {
    summary_enabled: true,
    question_generation_config: { enabled: true, question_count: 3, custom_instructions: '' },
    chunking_config: { chunk_size: 512 },
  }
  const current = {
    ...baseline,
    question_generation_config: { enabled: false, question_count: 3, custom_instructions: '' },
  }
  assert.deepEqual(diffProcessOverrides(baseline, current), {
    question_generation_config: { enabled: false, question_count: 3, custom_instructions: '' },
  })
})

test('diffProcessOverrides is empty when the form still matches the knowledge base', () => {
  const baseline = {
    question_generation_config: { enabled: false, question_count: 3, custom_instructions: '' },
  }
  assert.deepEqual(diffProcessOverrides(baseline, { ...baseline }), {})
})
