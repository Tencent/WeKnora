import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./EvaluationSettings.vue', import.meta.url), 'utf8')

test('evaluation form requires an explicit chat model and survives browser refreshes', () => {
  assert.match(source, /!form\.chat_id/)
  assert.match(source, /chatRequired/)
  assert.match(source, /sessionStorage\.setItem/)
  assert.match(source, /restoreForm/)
  assert.match(source, /resetForm/)
})

test('evaluation history identifies the model and source knowledge base', () => {
  assert.match(source, /runModelLabel\(run\)/)
  assert.match(source, /runKnowledgeBaseLabel\(run\)/)
  assert.match(source, /modelValue/)
  assert.match(source, /knowledgeBaseValue/)
})

test('invalid completed runs cannot enter a formal comparison', () => {
  assert.match(source, /isInvalidRun\(run\)/)
  assert.match(source, /:disabled="!isSelectableRun\(run\)"/)
  assert.match(source, /successful_calls === 0/)
})

test('comparison does not present unsupported cache or unpriced models as zero', () => {
  assert.match(source, /cache_reported_calls > 0/)
  assert.match(source, /Object\.prototype\.hasOwnProperty\.call\(costs, currency\)/)
  assert.doesNotMatch(source, /cost_by_currency\[currency\] \|\| 0/)
})

test('successful runs can export a server-generated evidence report', () => {
  assert.match(source, /getEvaluationEvidence/)
  assert.match(source, /exportEvidence\(run\.task\.id\)/)
  assert.match(source, /evaluation-evidence-\$\{taskID\}\.json/)
  assert.match(source, /JSON\.stringify\(report, null, 2\)/)
})
