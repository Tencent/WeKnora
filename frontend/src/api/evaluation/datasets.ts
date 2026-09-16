import { get, post } from '@/utils/request'
import type { EvaluationDetail, EvaluationTask } from './index'

export interface EvaluationDataset {
  id: string
  scope: 'system' | 'tenant'
  owner_tenant_id?: number
  name: string
  description: string
  current_version_id?: string
  created_at: string
  updated_at: string
}
export interface DatasetContent {
  passages: Array<{ pid: string; content: string; metadata?: Record<string, unknown> }>
  questions: Array<{ qid: string; question: string; answer: string }>
  relevance: Array<{ qid: string; pid: string; grade: number }>
}
export interface DatasetVersion {
  id: string
  dataset_id: string
  version_number: number
  schema_version: number
  artifact_sha256: string
  content_sha256: string
  manifest: Record<string, unknown>
  passage_count: number
  question_count: number
  relevance_count: number
  created_at: string
}
export interface DatasetLimits {
  max_request_body_bytes: number
  max_passages: number
  max_questions: number
  max_relevance: number
  max_question_bytes: number
  max_passage_bytes: number
  max_name_chars: number
  max_id_chars: number
  max_grade: number
}
export interface PublicDataset {
  id: string
  name: string
  description: string
  language: string
  source_url: string
  license: string
  counts: Record<string, number>
  limitations: string[]
}
export interface PublicDatasetPackage {
  id: string
  name: string
  description: string
  content: DatasetContent
  manifest: Record<string, unknown>
}
export interface DatasetImportRequest {
  request_id: string
  name: string
  description: string
  content: DatasetContent
}
export interface DatasetImportResult {
  dataset: EvaluationDataset
  version: DatasetVersion
  replayed: boolean
}
export interface CreateEvaluationRequest {
  dataset_id: string
  dataset_version_id: string
  knowledge_base_id: string
  chat_id: string
  rerank_id?: string
  seed?: number
  configuration?: {
    retrieval?: { embedding_top_k?: number }
    rerank?: { rerank_top_k?: number }
  }
}
interface Envelope<T> { success: boolean; data?: T }
function data<T>(response: Envelope<T>): T {
  if (!response?.success || response.data == null) throw new Error('Incomplete evaluation response')
  return response.data
}
const base = '/api/v1/evaluation/datasets'
export async function listEvaluationDatasets(): Promise<EvaluationDataset[]> {
  return data(await get<Envelope<{ items: EvaluationDataset[] }>>(base)).items
}
export async function listDatasetVersions(id: string): Promise<DatasetVersion[]> {
  return data(await get<Envelope<{ items: DatasetVersion[] }>>(`${base}/${encodeURIComponent(id)}/versions`)).items
}
export async function createDatasetVersion(id: string, content: DatasetContent): Promise<DatasetVersion> {
  return data(await post<Envelope<DatasetVersion>>(`${base}/${encodeURIComponent(id)}/versions`, content))
}
export async function getPublicDatasetCatalog(): Promise<{ items: PublicDataset[]; limits: DatasetLimits }> {
  return data(await get<Envelope<{ items: PublicDataset[]; limits: DatasetLimits }>>(`${base}/catalog`))
}
export async function getPublicDataset(id: string): Promise<PublicDatasetPackage> {
  return data(await get<Envelope<PublicDatasetPackage>>(`${base}/catalog/${encodeURIComponent(id)}`))
}
export async function importEvaluationDataset(request: DatasetImportRequest): Promise<DatasetImportResult> {
  const result = data(await post<Envelope<DatasetImportResult>>(`${base}/import`, request))
  if (!result.dataset?.id || !result.version?.id) throw new Error('Dataset import returned incomplete data')
  return result
}
export async function createEvaluation(request: CreateEvaluationRequest): Promise<EvaluationTask> {
  const detail = data(await post<Envelope<EvaluationDetail>>('/api/v1/evaluation', request))
  if (typeof detail.task?.id !== 'string' || !detail.task.id) throw new Error('Create evaluation returned no task')
  return detail.task
}
