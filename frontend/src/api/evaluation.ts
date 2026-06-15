import { del, get, post, put } from '@/utils/request'

export type MetricStatus = 'scored' | 'not_applicable' | 'failed' | 'skipped'
export interface ReferenceContext { text: string; knowledge_id?: string; chunk_id?: string }
export interface MetricDefinition { name: string; version: string; category: string; higher_is_better: boolean; requires_reference_answer: boolean; requires_reference_contexts: boolean }
export interface MetricScore { name: string; version: string; category: string; score: number | null; status: MetricStatus; higher_is_better: boolean; reason: string; error: string; scored_sample_count?: number; total_sample_count?: number }
export interface EvaluationDataset { id: string; name: string; description: string; sample_count: number; created_at: string; updated_at: string }
export interface EvaluationSample { id: string; dataset_id: string; question: string; reference_answer: string; reference_contexts: ReferenceContext[]; created_at: string }
export interface EvaluationRun { id: string; dataset_id: string; dataset_name: string; status: 'pending' | 'running' | 'completed' | 'failed'; config_snapshot: Record<string, any>; aggregate_metric_scores: Record<string, MetricScore>; total_samples: number; finished_samples: number; failed_samples: number; error: string; created_at: string }
export interface EvaluationRunResult { id: string; sample_id: string; sample_index: number; question: string; reference_answer: string; reference_contexts: ReferenceContext[]; retrieved_contexts: Array<ReferenceContext & { score: number; rank: number }>; generated_answer: string; status: string; error: string; metric_scores: Record<string, MetricScore>; duration_ms: number }
export interface PageResult<T> { total: number; page: number; page_size: number; data: T[] }
export interface RunComparison { baseline_run_id: string; candidate_run_id: string; dataset_id: string; metrics: Array<{ name: string; version: string; baseline_score: number; candidate_score: number; delta: number; improved: boolean; comparable_sample_count: number; sample_deltas: Array<{ sample_id: string; baseline_score: number; candidate_score: number; delta: number }> }> }

const unwrap = <T>(response: any): T => response.data as T
export const listEvaluationMetrics = () => get('/api/v1/evaluation/metrics').then(unwrap<MetricDefinition[]>)
export const listEvaluationDatasets = (page = 1, pageSize = 50) => get(`/api/v1/evaluation/datasets?page=${page}&page_size=${pageSize}`).then(unwrap<PageResult<EvaluationDataset>>)
export const createEvaluationDataset = (data: { name: string; description: string }) => post('/api/v1/evaluation/datasets', data).then(unwrap<EvaluationDataset>)
export const updateEvaluationDataset = (id: string, data: Partial<{ name: string; description: string }>) => put(`/api/v1/evaluation/datasets/${id}`, data).then(unwrap<EvaluationDataset>)
export const deleteEvaluationDataset = (id: string) => del(`/api/v1/evaluation/datasets/${id}`)
export const listEvaluationSamples = (datasetId: string, page = 1, pageSize = 50) => get(`/api/v1/evaluation/datasets/${datasetId}/samples?page=${page}&page_size=${pageSize}`).then(unwrap<PageResult<EvaluationSample>>)
export const createEvaluationSample = (datasetId: string, data: { question: string; reference_answer: string; reference_contexts: ReferenceContext[] }) => post(`/api/v1/evaluation/datasets/${datasetId}/samples`, data).then(unwrap<EvaluationSample>)
export const updateEvaluationSample = (datasetId: string, id: string, data: Partial<{ question: string; reference_answer: string; reference_contexts: ReferenceContext[] }>) => put(`/api/v1/evaluation/datasets/${datasetId}/samples/${id}`, data).then(unwrap<EvaluationSample>)
export const deleteEvaluationSample = (datasetId: string, id: string) => del(`/api/v1/evaluation/datasets/${datasetId}/samples/${id}`)
export const listEvaluationRuns = (page = 1, pageSize = 50) => get(`/api/v1/evaluation/runs?page=${page}&page_size=${pageSize}`).then(unwrap<PageResult<EvaluationRun>>)
export const createEvaluationRun = (data: Record<string, any>) => post('/api/v1/evaluation/runs', data).then(unwrap<EvaluationRun>)
export const getEvaluationRun = (id: string) => get(`/api/v1/evaluation/runs/${id}`).then(unwrap<EvaluationRun>)
export const listEvaluationRunResults = (id: string, page = 1, pageSize = 50) => get(`/api/v1/evaluation/runs/${id}/results?page=${page}&page_size=${pageSize}`).then(unwrap<PageResult<EvaluationRunResult>>)
export const compareEvaluationRuns = (baseline: string, candidate: string) => get(`/api/v1/evaluation/comparisons?baseline_run_id=${encodeURIComponent(baseline)}&candidate_run_id=${encodeURIComponent(candidate)}`).then(unwrap<RunComparison>)
