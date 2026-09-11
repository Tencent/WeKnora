import { get, post } from '@/utils/request'
import { assertSupportedTrainingProjection } from './trainingOrchestrationContract'

export { trainingJobErrorMessage, trainingJobWarningMessage } from './trainingOrchestrationErrors'

export type TrainingRelationType = 'required_before' | 'recommended_before' | 'application' | 'complementary' | 'contrast'
export type KnowledgeType = 'entity' | 'concept' | 'case' | 'methodology' | 'insight'
export type TrainingSkipReason = 'processing' | 'processing_failed' | 'knowledge_not_ready' | 'knowledge_audit_failed' | 'evidence_missing' | 'inaccessible'
export type TrainingNotSelectedReason = 'redundant_evidence' | 'output_limit'
export type TrainingTopicSource = 'final_summary' | 'normalized_transcript'
export type TrainingJobStage = 'collecting' | 'planning' | 'materializing' | 'generating' | 'assembling' | 'publishing'

export interface TrainingEvidenceRef {
  video_id: string
  transcript_generation: string
  evidence_id: string
  start_ms: number
  end_ms: number
}

export interface TrainingKnowledgeRef {
  knowledge_object_id: string
  wiki_page_id: string
  knowledge_type: KnowledgeType
}

export interface TrainingLearningUnit {
  unit_id: string
  learning_title: string
  learner_question: string
  learning_outcome: string
  sequence: number
  knowledge_refs: TrainingKnowledgeRef[]
  evidence_refs: TrainingEvidenceRef[]
  confidence: number
  review_status: 'passed'
}

export interface TrainingLearningStage {
  stage_id: string
  title: string
  summary: string
  sequence: number
  units: TrainingLearningUnit[]
}

export interface TrainingTopicCluster {
  cluster_id: string
  title: string
  summary: string
  learning_goal: string
  learning_content_type: string
  member_topics: Array<{ topic_id: string; title: string }>
  source_video_ids: string[]
  knowledge_object_ids: string[]
  evidence_refs: TrainingEvidenceRef[]
  confidence: number
  review_status: 'passed'
  path: { path_id: string; primary_template: string; stages: TrainingLearningStage[] }
}

export interface TrainingProjection {
  schema_version: 'training-orchestration/v1'
  owner_scope_id: string
  source_fingerprint: string
  generated_at: string
  retrieval_degraded?: boolean
  retrieval_degradation_reason?: string
  statistics: {
    scanned_videos: number
    qualified_videos: number
    selected_videos: number
    not_selected_videos: number
    not_selected_reason_counts: Record<TrainingNotSelectedReason, number>
    skipped_videos: number
    skipped_reason_counts: Record<TrainingSkipReason, number>
    topic_source_counts: Record<TrainingTopicSource, number>
    topic_cluster_count: number
    selected_knowledge_count: number
    learning_duration_seconds: number
  }
  topic_clusters: TrainingTopicCluster[]
  topic_cluster_relations: Array<{
    relation_id: string
    source_cluster_id: string
    target_cluster_id: string
    relation_type: TrainingRelationType
    summary: string
    source_knowledge_refs: string[]
    target_knowledge_refs: string[]
    source_evidence_refs: TrainingEvidenceRef[]
    target_evidence_refs: TrainingEvidenceRef[]
    confidence: number
    review_status: 'passed'
  }>
}

export interface TrainingJob {
  id: string
  status: 'queued' | 'running' | 'succeeded' | 'failed'
  stage?: TrainingJobStage
  progress: number
  result_wiki_page_id?: string
  error_code?: string
  error_message?: string
  warning_code?: string
  warning_message?: string
  reused: boolean
}

export async function fetchCurrentTrainingProjection(): Promise<TrainingProjection | null> {
  const response: any = await get('/api/custom/training-orchestration/current')
  const projection = response?.data?.training_path_projection
  if (!projection) return null
  assertSupportedTrainingProjection(projection)
  return projection as TrainingProjection
}

export async function generateTrainingProjection(): Promise<TrainingJob> {
  const response: any = await post('/api/custom/training-orchestration/generate')
  return response?.data
}

export async function fetchTrainingJob(id: string): Promise<TrainingJob> {
  const response: any = await get(`/api/custom/training-orchestration/jobs/${encodeURIComponent(id)}`)
  return response?.data
}
