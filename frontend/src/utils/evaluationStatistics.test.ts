import assert from 'node:assert/strict'
import test from 'node:test'
import { aggregateRepeatedRuns, median, percentileNearestRank } from './evaluationStatistics'
import type { EvaluationRunSummary } from '@/api/evaluation'

test('median and nearest-rank P95 preserve observed small-sample behavior', () => {
  assert.equal(median([30, 10, 20]), 20)
  assert.equal(median([10, 20, 30, 40]), 25)
  assert.equal(percentileNearestRank([30, 10, 20], 0.95), 30)
  assert.equal(median([undefined]), undefined)
})

function run(id: string, modelID: string, duration: number, recall: number, tokens: number): EvaluationRunSummary {
  return {
    task: { id, tenant_id: 1, dataset_id: 'fixed', start_time: `2026-09-08T00:00:0${id}Z`, duration_ms: duration, status: 2 },
    run_config: {
      dataset_fingerprint: 'dataset-a', controlled_fingerprint: 'controlled-a', code_version: 'commit-a',
      source_knowledge_base_id: 'kb-a',
      models: [{ role: 'chat', id: modelID, name: modelID, display_name: `Model ${modelID}`, config_fingerprint: `fp-${modelID}` }],
    },
    metric: { retrieval_metrics: { recall }, generation_metrics: {} },
    usage: {
      call_count: 1, successful_calls: 1, failed_calls: 0, prompt_tokens: tokens,
      completion_tokens: 0, total_tokens: tokens, cache_read_tokens: 0, cache_write_tokens: 0,
      cache_miss_tokens: tokens, cache_reported_calls: 1, cache_hit_calls: 0, cache_hit_rate: 0,
      cache_coverage_rate: 1, model_duration_ms: duration, average_model_latency_ms: duration,
      priced_calls: 1, unpriced_calls: 0, cost_by_currency: { CNY: tokens / 1000 },
    },
  }
}

test('repeated runs aggregate only exact code, data, configuration and model matches', () => {
  const repeated = [run('1', 'a', 30, 0.7, 300), run('2', 'a', 10, 0.9, 100), run('3', 'a', 20, 0.8, 200)]
  const differentModel = run('4', 'b', 5, 1, 50)
  const groups = aggregateRepeatedRuns([...repeated, differentModel])

  assert.equal(groups.length, 1)
  assert.equal(groups[0].model, 'Model a')
  assert.equal(groups[0].runs, 3)
  assert.equal(groups[0].recallMedian, 0.8)
  assert.equal(groups[0].durationMedianMS, 20)
  assert.equal(groups[0].durationP95MS, 30)
  assert.equal(groups[0].tokensMedian, 200)
  assert.deepEqual(groups[0].cost, { currency: 'CNY', median: 0.2 })
})

test('cold and warm runs with different model-call workloads are split', () => {
  const warmRuns = [run('1', 'a', 10, 0.8, 100), run('2', 'a', 12, 0.8, 100)]
  const coldRun = run('3', 'a', 30, 0.8, 100)
  coldRun.usage!.call_count = 2

  const groups = aggregateRepeatedRuns([...warmRuns, coldRun])

  assert.equal(groups.length, 1)
  assert.equal(groups[0].runs, 2)
  assert.equal(groups[0].durationMedianMS, 11)
  assert.equal(groups[0].durationP95MS, 12)
})
