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

test('repeated experiments expose matched-run median and P95 statistics', () => {
  assert.match(source, /aggregateRepeatedRuns\(runPage\.value\.items\)/)
  assert.match(source, /repeatSummary/)
  assert.match(source, /durationMedianMS/)
  assert.match(source, /durationP95MS/)
  assert.match(source, /latencyMedianWidth/)
})

test('repeated summaries and evaluation history use fixed pagination sizes', () => {
  assert.match(source, /const repeatPageSize = 2/)
  assert.match(source, /const historyPageSize = 5/)
  assert.match(source, /v-for="aggregate in pagedRepeatedAggregates"/)
  assert.match(source, /v-for="run in pagedHistoryRuns"/)
  assert.match(source, /v-model="repeatPage"/)
  assert.match(source, /v-model="historyPage"/)
  assert.match(source, /clampPage/)
})

test('strict Wiki cache benchmark discloses cost, validates pairs, and exports evidence', () => {
  assert.match(source, /runWikiCacheBenchmark/)
  assert.match(source, /cacheBenchmarkCostHint/)
  assert.match(source, /strict_validation\.passed/)
  assert.match(source, /coldCohort/)
  assert.match(source, /warmCohort/)
  assert.match(source, /wiki-cache-benchmark-\$\{cacheBenchmark\.value\.benchmark_id\}\.json/)
})
