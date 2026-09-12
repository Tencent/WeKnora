import type { EvaluationDetail } from '../../api/evaluation'

export interface SummaryMetric {
  id: string
  label: string
  value: number | null
  status: string
}

function record(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}
}

function finite(value: unknown): number | null {
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

// The frozen plan identifies each score. Missing dynamic observations must not
// fall back to the fixed numeric fields, whose default zero can mean unobserved.
export function summarizeEvaluation(detail: Pick<EvaluationDetail, 'metric' | 'experiment'> | null) {
  const result: Record<'retrieval' | 'generation', SummaryMetric[]> = { retrieval: [], generation: [] }
  const metric = record(detail?.metric)
  const plan = record(record(detail?.experiment).metric_plan).metrics
  if (Array.isArray(plan) || metric.scores != null) {
    const scores = record(metric.scores)
    const specs: Record<string, unknown>[] = Array.isArray(plan) ? plan.map(record) : Object.keys(scores).map(id => ({ instance_id: id, key: id.split('@')[0] }))
    for (const spec of specs) {
      const id = String(spec.instance_id || '')
      const key = String(spec.key || '')
      const group = key.startsWith('retrieval.') ? 'retrieval' : key.startsWith('generation.') ? 'generation' : null
      if (!group || !id) continue
      const score = record(scores[id])
      const config = record(spec.config)
      const suffix = Object.entries(config).map(([name, value]) => `${name}=${JSON.stringify(value)}`).join(', ')
      const label = `${key.slice(group.length + 1)}${suffix ? ` (${suffix})` : ''}${spec.version ? ` · v${spec.version}` : ''}`
      result[group].push({ id, label, value: score.status === 'valid' ? finite(score.value) : null, status: String(score.status || 'missing') })
    }
    return result
  }
  for (const group of ['retrieval', 'generation'] as const) {
    for (const [key, value] of Object.entries(record(metric[`${group}_metrics`]))) {
      result[group].push({ id: key, label: key.toUpperCase(), value: finite(value), status: finite(value) === null ? 'missing' : 'valid' })
    }
  }
  return result
}

export function summaryDuration(value: unknown): string {
  const milliseconds = finite(value)
  return milliseconds === null || milliseconds < 0 ? '—' : `${(milliseconds / 1000).toFixed(2)} s`
}

export function summaryCost(cost: NonNullable<EvaluationDetail['runtime_metrics']>['cost']): string {
  if (!cost?.totals?.length) return '—'
  return cost.totals.map(item => `${item.currency} ${(item.cost_microunits / 1_000_000).toFixed(6).replace(/\.?0+$/, '')}`).join(' · ')
}
