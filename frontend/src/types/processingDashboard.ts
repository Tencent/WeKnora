export type ProcessingLogicalStage =
  | 'docreader'
  | 'chunking'
  | 'embedding'
  | 'multimodal'
  | 'postprocess'
  | 'summary'
  | 'question'
  | 'graph'
  | 'wiki'

export type ProcessingStageState =
  | 'not_reached'
  | 'queued'
  | 'running'
  | 'retrying'
  | 'done'
  | 'done_with_errors'
  | 'failed'
  | 'skipped'
  | 'cancelled'

export interface ProcessingStageProgress {
  completed: number
  total: number
  failed: number
  unit: string
  reliable: boolean
}

export interface ProcessingStageItem {
  knowledge_id: string
  knowledge_base_id: string
  knowledge_base_name: string
  title: string
  attempt: number
  stage: ProcessingLogicalStage
  state: ProcessingStageState
  progress?: ProcessingStageProgress | null
  phase?: string
  started_at?: string
  queued_at?: string
  next_retry_at?: string
  last_progress_at?: string
  finished_at?: string
  elapsed_ms?: number
  duration_ms?: number
  failed_children?: number
  error_code?: string
  error_message?: string
  skip_reason?: string
}

export interface ProcessingStageSummary {
  key: ProcessingLogicalStage
  group: 'primary' | 'enrichment' | string
  order: number
  title: string
  description: string
  running_count: number
  queued_count: number
  retrying_count: number
  retrying_observable: boolean
  completion_reliable: boolean
  counts_reliable: boolean
  running_items: ProcessingStageItem[]
}

export interface ProcessingDashboardSource {
  executor_mode: string
  queue_snapshot: 'ok' | 'partial' | 'degraded' | 'not_applicable' | string
  truncated_queues?: string[]
  message?: string
}

export interface ProcessingStageGroup {
  key: string
  name: string
  stages: ProcessingLogicalStage[]
}

export interface ProcessingDashboardResponse {
  generated_at: string
  source: ProcessingDashboardSource
  filters: {
    knowledge_base_id: string
    keyword: string
  }
  groups: ProcessingStageGroup[]
  stages: ProcessingStageSummary[]
}

export interface ProcessingStageItemsResponse {
  generated_at: string
  source: ProcessingDashboardSource
  stage: ProcessingLogicalStage
  state: ProcessingStageState
  items: ProcessingStageItem[]
  next_cursor: string
  total: number
}

export interface ProcessingKnowledgeDetailResponse {
  generated_at: string
  source: ProcessingDashboardSource
  knowledge: any
  current_attempt: number
  parse_status: string
  pending_subtasks_count: number
  stages: ProcessingStageItem[]
  raw_trace: any[]
}

export const PROCESSING_STAGE_ORDER: ProcessingLogicalStage[] = [
  'docreader',
  'chunking',
  'embedding',
  'multimodal',
  'postprocess',
  'summary',
  'question',
  'graph',
  'wiki',
]
