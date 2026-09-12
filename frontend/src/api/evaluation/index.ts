import { get, getDown, post, put } from '@/utils/request'

import {
  buildEvaluationTaskQuery,
  normalizeEvaluationComparisonSelection,
  type EvaluationTaskFilters,
} from './query'

export {
  buildEvaluationTaskQuery,
  normalizeEvaluationComparisonSelection,
  type EvaluationTaskFilters,
} from './query'

export { createEvaluationRequestGate, type EvaluationRequestGate, type EvaluationRequestToken } from './requestGate'

export const EVALUATION_STATUS = {
  pending: 0,
  running: 1,
  success: 2,
  failed: 3,
  timedOut: 4,
  interrupted: 5,
  canceled: 6,
} as const

export interface EvaluationTask {
  id: string
  tenant_id: number
  dataset_id: string
  dataset_version_id?: string
  provenance_complete: boolean
  status: number
  start_time: string
  end_time?: string
  err_msg?: string
  cleanup_errors?: string[]
  cancel_requested_at?: string
  labels: string[]
  total?: number
  finished?: number
}

export interface EvaluationTaskPage {
  items: EvaluationTask[]
  next_cursor: string
}

export interface EvaluationDetail {
  task: EvaluationTask
  params?: Record<string, unknown>
  metric?: Record<string, unknown>
  experiment?: Record<string, unknown> | null
  runtime_metrics?: EvaluationRuntimeMetrics | null
  provenance_complete: boolean
}

export interface EvaluationRuntimeMetrics {
  schema_version: number
  started_at: string
  ended_at?: string
  durations: {
    dataset_load_ms?: number
    indexing_ms?: number
    execution_ms?: number
    persistence_ms?: number
    cleanup_ms?: number
    total_ms?: number
  }
  samples: { total: number; started: number; success: number; failed: number; canceled: number; interrupted: number; not_started: number }
  failure: { numerator: number; denominator: number }
  tokens: { prompt_tokens: number; completion_tokens: number; total_tokens: number; reported_samples: number; unreported_samples: number }
  cost?: {
    call_count: number
    accounting_complete_calls: number
    unpriced_calls: number
    usage_unreported_calls: number
    started_calls: number
    totals: Array<{ currency: string; cost_microunits: number }>
  }
}

export interface EvaluationRankedResult {
  rank: number
  pid: number
  score: number
  provenance: string
}

export interface EvaluationQuestionResult {
  sample_index: number
  qid: string
  question: string
  reference_answer: string
  ground_truth_pids: number[]
  search_results: EvaluationRankedResult[]
  rerank_results: EvaluationRankedResult[]
  generation_pids: number[]
  generated_text: string
  per_sample_metrics: Record<string, unknown>
  metric_observations: Array<Record<string, unknown>>
  error_code?: string
  status: string
  retrieval_ms?: number
  rerank_ms?: number
  generation_ms?: number
  total_ms?: number
  prompt_tokens?: number
  completion_tokens?: number
  total_tokens?: number
}

export interface EvaluationQuestionPage {
  items: EvaluationQuestionResult[]
  next_cursor: string
}

export interface EvaluationComparisonRequest {
  task_ids: string[]
  baseline_task_id?: string
}

export interface EvaluationConfidenceInterval {
  estimate: number
  lower: number
  upper: number
  confidence: number
  method: string
  samples: number
  iterations?: number
  seed?: number
}

export interface EvaluationPercentiles {
  p50: number
  p95: number
  p99: number
  n_total: number
  n_valid: number
  n_missing: number
}

export interface EvaluationComparisonParameterValue {
  task_id: string
  missing: boolean
  value?: unknown
}

export interface EvaluationComparisonParameter {
  pointer: string
  differ: boolean
  values: EvaluationComparisonParameterValue[]
}

export interface EvaluationComparisonMetricValue {
  task_id: string
  is_baseline: boolean
  status: string
  value: number | null
  delta: number | null
  relative_delta: number | null
  relative_reason?: string
  reason?: string
  confidence?: EvaluationConfidenceInterval
  confidence_status: string
  n_total: number
  n_valid: number
  n_missing: number
}

export interface EvaluationComparisonMetric {
  pointer: string
  key: string
  version: string
  config_sha256: string
  compatible: boolean
  baseline_task_id: string
  values: EvaluationComparisonMetricValue[]
}

export interface EvaluationComparisonResponse {
  schema_version: number
  baseline_task_id: string
  runs: Array<{
    task_id: string
    status: number
    is_baseline: boolean
    dataset_id: string
    dataset_version_id: string
    version_number: number
    dataset_content_sha256: string
    provenance_complete: boolean
    question_success_rate?: EvaluationConfidenceInterval
    question_success_status: string
    question_n_total: number
    question_n_valid: number
    question_n_missing: number
    total_latency_ms?: EvaluationPercentiles
    token_totals: {
      prompt: number
      completion: number
      total: number
      n_total: number
      n_valid: number
      n_missing: number
    }
  }>
  parameters: EvaluationComparisonParameter[]
  metrics: EvaluationComparisonMetric[]
}

export interface EvaluationHumanRatingRevision {
  id: string
  tenant_id: number
  task_id: string
  sample_index: number
  revision: number
  rater_id: string
  rubric_key: string
  rubric_version: string
  rubric_snapshot: Record<string, unknown>
  score: number
  comment?: string
  supersedes_id?: string
  created_at: string
}

export interface AppendEvaluationHumanRatingRequest {
  rubric_key: string
  rubric_version: string
  rubric_snapshot: Record<string, unknown>
  score: number
  comment?: string
}

interface Envelope<T> {
  success: boolean
  data?: T
}

function requireEvaluationData<T>(response: Envelope<T>, operation: string): T {
  if (!response?.success || response.data === undefined || response.data === null) {
    throw new Error(`${operation} returned an incomplete response`)
  }
  return response.data
}

export async function listEvaluationTasks(filters: EvaluationTaskFilters): Promise<EvaluationTaskPage> {
  const query = buildEvaluationTaskQuery(filters)
  const response = await get<Envelope<EvaluationTaskPage>>(`/api/v1/evaluation/tasks?${query.toString()}`)
  const data = requireEvaluationData(response, 'List evaluations')
  if (!Array.isArray(data.items) || typeof data.next_cursor !== 'string') {
    throw new Error('List evaluations returned an incomplete page')
  }
  return data
}

export async function getEvaluationDetail(taskId: string): Promise<EvaluationDetail> {
  const query = new URLSearchParams({ task_id: taskId })
  const response = await get<Envelope<EvaluationDetail>>(`/api/v1/evaluation?${query.toString()}`)
  const data = requireEvaluationData(response, 'Get evaluation')
  if (!data.task) throw new Error('Get evaluation returned no task')
  return data
}

export async function listEvaluationQuestions(
  taskId: string,
  cursor = '',
  pageSize = 100,
): Promise<EvaluationQuestionPage> {
  const query = new URLSearchParams({ page_size: String(pageSize) })
  if (cursor) query.set('cursor', cursor)
  const response = await get<Envelope<EvaluationQuestionPage>>(
    `/api/v1/evaluation/tasks/${encodeURIComponent(taskId)}/questions?${query.toString()}`,
  )
  const data = requireEvaluationData(response, 'List evaluation questions')
  if (!Array.isArray(data.items) || typeof data.next_cursor !== 'string') {
    throw new Error('List evaluation questions returned an incomplete page')
  }
  return data
}

export async function listEvaluationHumanRatings(
  taskId: string,
  sampleIndex: number,
): Promise<EvaluationHumanRatingRevision[]> {
  const response = await get<Envelope<{ items: EvaluationHumanRatingRevision[] }>>(
    `/api/v1/evaluation/tasks/${encodeURIComponent(taskId)}/questions/${sampleIndex}/ratings`,
  )
  const data = requireEvaluationData(response, 'List evaluation human ratings')
  if (!Array.isArray(data.items)) throw new Error('List evaluation human ratings returned no items')
  return data.items
}

export async function appendEvaluationHumanRating(
  taskId: string,
  sampleIndex: number,
  request: AppendEvaluationHumanRatingRequest,
): Promise<EvaluationHumanRatingRevision> {
  const response = await post<Envelope<EvaluationHumanRatingRevision>>(
    `/api/v1/evaluation/tasks/${encodeURIComponent(taskId)}/questions/${sampleIndex}/ratings`,
    request,
  )
  return requireEvaluationData(response, 'Append evaluation human rating')
}

export async function replaceEvaluationLabels(taskId: string, labels: string[]): Promise<string[]> {
  const response = await put<Envelope<{ task_id: string; labels: string[] }>>(
    `/api/v1/evaluation/tasks/${encodeURIComponent(taskId)}/labels`,
    { labels },
  )
  const data = requireEvaluationData(response, 'Replace evaluation labels')
  if (!Array.isArray(data.labels)) throw new Error('Replace evaluation labels returned no labels')
  return data.labels
}

export async function compareEvaluationTasks(
  request: EvaluationComparisonRequest,
): Promise<EvaluationComparisonResponse> {
  const taskIds = normalizeEvaluationComparisonSelection(request.task_ids)
  if (taskIds.length < 2) throw new Error('Select at least 2 evaluation tasks')
  const response = await post<Envelope<EvaluationComparisonResponse>>('/api/v1/evaluation/comparisons', {
    task_ids: taskIds,
    baseline_task_id: request.baseline_task_id,
  })
  const data = requireEvaluationData(response, 'Compare evaluations')
  if (!Array.isArray(data.runs) || !Array.isArray(data.parameters) || !Array.isArray(data.metrics)) {
    throw new Error('Compare evaluations returned incomplete data')
  }
  return data
}

export const EVALUATION_EXPORT_TIMEOUT_MS = 10 * 60 * 1000

export interface EvaluationExportOptions {
  signal?: AbortSignal
  timeoutMs?: number
}

export async function downloadEvaluationArtifact(
  taskId: string,
  format: 'json' | 'csv',
  options: EvaluationExportOptions = {},
): Promise<void> {
  const blob = await getDown(
    `/api/v1/evaluation/tasks/${encodeURIComponent(taskId)}/export?format=${format}`,
    {
      signal: options.signal,
      timeout: options.timeoutMs ?? EVALUATION_EXPORT_TIMEOUT_MS,
    },
  )
  const objectURL = URL.createObjectURL(blob)
  try {
    const anchor = document.createElement('a')
    anchor.href = objectURL
    anchor.download = `evaluation-${taskId.replace(/[^a-zA-Z0-9_-]/g, '_')}.${format}`
    anchor.click()
  } finally {
    URL.revokeObjectURL(objectURL)
  }
}
