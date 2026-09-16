import type { EvaluationRunSummary } from '@/api/evaluation'

export interface RepeatRunAggregate {
  key: string
  model: string
  runs: number
  latestStartTime: string
  recallMedian?: number
  durationMedianMS?: number
  durationP95MS?: number
  tokensMedian?: number
  cacheHitRateMedian?: number
  cost?: { currency: string; median: number }
}

export function median(values: Array<number | undefined>): number | undefined {
  const sorted = finiteValues(values)
  if (!sorted.length) return undefined
  const middle = Math.floor(sorted.length / 2)
  return sorted.length % 2 ? sorted[middle] : (sorted[middle - 1] + sorted[middle]) / 2
}

// Nearest-rank is deliberately used for small repeated experiments: the P95
// of three runs is the slowest observation instead of an interpolated value
// that was never measured.
export function percentileNearestRank(values: Array<number | undefined>, percentile: number): number | undefined {
  const sorted = finiteValues(values)
  if (!sorted.length) return undefined
  const bounded = Math.min(1, Math.max(0, percentile))
  return sorted[Math.max(0, Math.ceil(bounded * sorted.length) - 1)]
}

export function aggregateRepeatedRuns(runs: EvaluationRunSummary[]): RepeatRunAggregate[] {
  const groups = new Map<string, EvaluationRunSummary[]>()
  for (const run of runs) {
    const config = run.run_config
    const chatModel = config?.models?.find(model => model.role === 'chat')
    if (run.task.status !== 2 || !run.metric || !run.usage || run.usage.successful_calls === 0 ||
      !config?.dataset_fingerprint || !config.controlled_fingerprint || !config.code_version || !chatModel) continue
    // Identical configuration is not sufficient for a repeated latency sample:
    // a cold index build can include embedding provider work that later durable
    // cache hits avoid. Keep different observed call workloads in separate
    // cohorts instead of presenting their end-to-end latency as interchangeable.
    const key = [config.code_version, config.dataset_fingerprint, config.controlled_fingerprint,
      config.source_knowledge_base_id || '', chatModel.config_fingerprint || chatModel.id,
      run.usage.call_count, run.usage.successful_calls, run.usage.failed_calls,
      run.usage.cache_reported_calls].join('|')
    groups.set(key, [...(groups.get(key) || []), run])
  }

  return [...groups.entries()].flatMap(([key, group]) => {
    if (group.length < 2) return []
    const first = group[0]
    const model = first.run_config!.models!.find(item => item.role === 'chat')!
    const pricedCurrencies = new Set(group.flatMap(run => Object.keys(run.usage?.cost_by_currency || {})))
    const currency = pricedCurrencies.size === 1 ? [...pricedCurrencies][0] : undefined
    return [{
      key,
      model: model.display_name || model.name,
      runs: group.length,
      latestStartTime: group.map(run => run.task.start_time).sort().at(-1) || '',
      recallMedian: median(group.map(run => run.metric?.retrieval_metrics.recall)),
      durationMedianMS: median(group.map(run => run.task.duration_ms)),
      durationP95MS: percentileNearestRank(group.map(run => run.task.duration_ms), 0.95),
      tokensMedian: median(group.map(run => run.usage?.total_tokens)),
      cacheHitRateMedian: median(group.map(run => run.usage && run.usage.cache_reported_calls > 0
        ? run.usage.cache_hit_rate : undefined)),
      cost: currency ? { currency, median: median(group.map(run => run.usage?.cost_by_currency?.[currency]))! } : undefined,
    }]
  }).sort((left, right) => right.latestStartTime.localeCompare(left.latestStartTime))
}

function finiteValues(values: Array<number | undefined>): number[] {
  return values.filter((value): value is number => Number.isFinite(value)).sort((left, right) => left - right)
}
