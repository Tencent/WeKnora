import { get, post } from '../../utils/request'

export interface EvaluationUsage {
  call_count: number
  successful_calls: number
  failed_calls: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  cache_read_tokens: number
  cache_write_tokens: number
  cache_miss_tokens: number
  cache_reported_calls: number
  cache_hit_calls: number
  cache_hit_rate: number
  cache_coverage_rate: number
  model_duration_ms: number
  average_model_latency_ms: number
  priced_calls: number
  unpriced_calls: number
  cost_by_currency: Record<string, number>
}

export interface EvaluationModelUsageStat {
  model_id: string
  model_name: string
  model_type: string
  usage: EvaluationUsage
  purposes?: EvaluationPurposeUsageStat[]
  embedding_cache?: EmbeddingCacheUsageStat
}

export interface EmbeddingCacheUsageStat {
  lookup_count: number
  hit_count: number
  miss_count: number
  deduplicated_count: number
  avoided_computations: number
  hit_rate: number
  avoided_rate: number
}

export interface EvaluationPurposeUsageStat {
  purpose: string
  usage: EvaluationUsage
}

export interface EvaluationModelUsageRange {
  startTime?: string
  endTime?: string
}

export interface EvaluationDataset {
  schema_version: number
  id: string
  name: string
  description?: string
  language?: string
  scenario?: string
  source?: string
  license?: string
  created_at?: string
  coverage_dimensions?: string[]
  available: boolean
  validation_error?: string
}

export interface EvaluationTask {
  id: string
  tenant_id: number
  dataset_id: string
  start_time: string
  end_time?: string
  duration_ms: number
  status: number
  err_msg?: string
  total?: number
  finished?: number
}

export interface EvaluationMetricResult {
  retrieval_metrics: Record<string, number>
  generation_metrics: Record<string, number>
  samples?: Array<Record<string, unknown>>
}

export interface EvaluationEvidenceReport {
  schema_version: number
  report_sha256: string
  task: EvaluationTask
  run_config?: EvaluationRunSummary['run_config']
  metric?: EvaluationMetricResult
  usage?: EvaluationUsage
  model_calls?: Array<Record<string, unknown>>
  warnings?: string[]
}

export interface WikiCacheBenchmarkCohort {
  model_calls: Array<Record<string, unknown>>
  usage: EvaluationUsage
  median_latency_ms: number
  p95_latency_ms: number
}

export interface WikiCacheBenchmarkEvidence {
  schema_version: number
  benchmark_id: string
  generated_at: string
  code_version: string
  model_id: string
  model_name: string
  repetitions: number
  workload_sha256: string
  configuration_sha256: string
  cold: WikiCacheBenchmarkCohort
  warm: WikiCacheBenchmarkCohort
  strict_validation: { passed: boolean; reason?: string }
  warnings?: string[]
  report_sha256: string
}

export interface EvaluationRunSummary {
  task: EvaluationTask
  run_config?: {
    dataset_id?: string
    dataset_fingerprint?: string
    dataset_samples?: number
    code_version?: string
    config_fingerprint?: string
    controlled_fingerprint?: string
    source_knowledge_base_id?: string
    chunking?: Record<string, unknown>
    pipeline?: Record<string, unknown>
    models?: Array<{
      role: string
      id: string
      name: string
      display_name?: string
      config_fingerprint?: string
    }>
  }
  metric?: EvaluationMetricResult
  usage?: EvaluationUsage
}

export interface EvaluationRunPage {
  items: EvaluationRunSummary[]
  total: number
  limit: number
  offset: number
}

export interface StartEvaluationRequest {
  dataset_id?: string
  knowledge_base_id?: string
  chat_id?: string
  rerank_id?: string
}

export async function getEvaluationDatasets(): Promise<EvaluationDataset[]> {
  const response: any = await get('/api/v1/evaluation/datasets')
  return response?.success && Array.isArray(response.data) ? response.data : []
}

export async function getEvaluationRuns(limit = 50, offset = 0): Promise<EvaluationRunPage> {
  const response: any = await get('/api/v1/evaluation/runs', { params: { limit, offset } })
  if (response?.success && response.data) return response.data
  return { items: [], total: 0, limit, offset }
}

// Evaluation summaries omit per-sample evidence and are small enough to load in
// bounded API pages. Keeping the complete tenant history client-side preserves
// cross-page comparison selections and lets repeated-run cohorts use all runs.
export async function getAllEvaluationRuns(): Promise<EvaluationRunPage> {
  const pageSize = 100
  const first = await getEvaluationRuns(pageSize, 0)
  const items = [...first.items]
  let offset = first.items.length

  while (offset < first.total) {
    const page = await getEvaluationRuns(pageSize, offset)
    if (!page.items.length) break
    items.push(...page.items)
    offset += page.items.length
  }

  return { items, total: first.total, limit: items.length, offset: 0 }
}

export async function startEvaluation(request: StartEvaluationRequest): Promise<EvaluationRunSummary> {
  const response: any = await post('/api/v1/evaluation', request)
  if (!response?.success || !response.data) throw new Error('Invalid evaluation response')
  return response.data
}

export async function getEvaluationResult(taskID: string): Promise<EvaluationRunSummary> {
  const response: any = await get('/api/v1/evaluation', { params: { task_id: taskID } })
  if (!response?.success || !response.data) throw new Error('Invalid evaluation response')
  return response.data
}

export async function getEvaluationEvidence(taskID: string): Promise<EvaluationEvidenceReport> {
  const response: any = await get('/api/v1/evaluation/evidence', { params: { task_id: taskID } })
  if (!response?.report_sha256 || !response.task) throw new Error('Invalid evaluation evidence response')
  return response
}

export async function getEvaluationModelUsage(range: EvaluationModelUsageRange = {}): Promise<EvaluationModelUsageStat[]> {
  const response: any = await get('/api/v1/evaluation/model-usage', {
    params: {
      ...(range.startTime ? { start_time: range.startTime } : {}),
      ...(range.endTime ? { end_time: range.endTime } : {}),
    },
  })
  if (!response?.success || !Array.isArray(response.data)) return []
  return response.data
}

export async function runWikiCacheBenchmark(chatModelID: string): Promise<WikiCacheBenchmarkEvidence> {
  const response: any = await post('/api/v1/evaluation/wiki-cache-benchmark', { chat_id: chatModelID })
  if (!response?.report_sha256 || !response?.cold || !response?.warm) {
    throw new Error('Invalid Wiki cache benchmark response')
  }
  return response
}
