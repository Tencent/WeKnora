import assert from 'node:assert/strict'
import test from 'node:test'
import { summarizeEvaluation, summaryCost, summaryDuration } from './evaluationSummary'

test('frozen metric instances retain zero and distinguish absent or failed observations', () => {
  const result = summarizeEvaluation({
    experiment: { metric_plan: { metrics: [
      { key: 'retrieval.recall', instance_id: 'recall', version: '1.0.0', config: {} },
      { key: 'generation.rouge', instance_id: 'rouge1', version: '1.0.0', config: { variant: 'rouge-1' } },
      { key: 'generation.rouge', instance_id: 'rougeL', version: '1.0.0', config: { variant: 'rouge-l' } },
    ] } },
    metric: { scores: { recall: { status: 'valid', value: 0 }, rouge1: { status: 'failed', value: 0.9 } }, generation_metrics: { rougel: 0 } },
  })
  assert.equal(result.retrieval[0].value, 0)
  assert.deepEqual(result.generation.map(metric => metric.value), [null, null])
  assert.notEqual(result.generation[0].label, result.generation[1].label)
  assert.match(result.retrieval[0].label, /v1.0.0/)
})

test('a frozen plan without scores never displays default fixed zero as measured quality', () => {
  const result = summarizeEvaluation({
    experiment: { metric_plan: { metrics: [{ key: 'retrieval.recall', instance_id: 'recall' }] } },
    metric: { retrieval_metrics: { recall: 0 } },
  })
  assert.equal(result.retrieval[0].value, null)
})

test('scores without a plan and fixed metrics preserve available finite values', () => {
  assert.equal(summarizeEvaluation({ metric: { scores: { 'retrieval.recall@1#x': { status: 'valid', value: 0.4 } } } }).retrieval[0].value, 0.4)
  assert.deepEqual(summarizeEvaluation({ metric: { retrieval_metrics: { recall: 0, precision: null, mrr: Number.NaN } } }).retrieval.map(x => x.value), [0, null, null])
  assert.deepEqual(summarizeEvaluation(null), { retrieval: [], generation: [] })
})

test('runtime summaries preserve zero, missing durations and separate currencies', () => {
  assert.equal(summaryDuration(0), '0.00 s')
  assert.equal(summaryDuration(1234), '1.23 s')
  for (const value of [null, undefined, Number.NaN, -1]) assert.equal(summaryDuration(value), '—')
  assert.equal(summaryCost(undefined), '—')
  assert.equal(summaryCost({ call_count: 2, accounting_complete_calls: 1, unpriced_calls: 1, usage_unreported_calls: 0, started_calls: 0, totals: [
    { currency: 'USD', cost_microunits: 0 }, { currency: 'CNY', cost_microunits: 10000001 },
  ] }), 'USD 0 · CNY 10.000001')
})
