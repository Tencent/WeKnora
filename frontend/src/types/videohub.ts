export type VideoCategory = 'interview' | 'training' | 'salon' | 'general'

export interface KnowledgePoint {
  id: string
  title: string
  timestamp: string
  seconds: number
  transcriptSnippet?: string
}

export type ChapterAlignmentStatus = 'verified' | 'aligned' | 'pending_alignment'

export interface ChapterSourceSegment {
  speaker: string
  start_time: string
  start_seconds: number
  end_time: string
  end_seconds: number
  content: string
}

export interface Chapter {
  id: string
  chapter_index: string
  chapter_title: string
  start_time: string
  start_seconds: number
  end_time: string
  end_seconds: number
  chapter_summary: string
  knowledge_points: KnowledgePoint[]
  alignment_status?: ChapterAlignmentStatus
  source_content?: ChapterSourceSegment[]
}

export interface SubtitleCue {
  start_seconds: number
  end_seconds: number
  text: string
}

export interface SummaryEvidence {
  chunkId: string
  startSeconds: number
  endSeconds: number
  timestamp: string
  transcriptSnippet: string
}

export type SummaryBlockKind = 'paragraph' | 'bullet'

export interface SummaryBlock {
  id: string
  kind: SummaryBlockKind
  text: string
  evidence: SummaryEvidence[]
}

export interface SummarySection {
  id: string
  title: string
  blocks: SummaryBlock[]
}

export type ContentLoadStatus = 'loading' | 'ready' | 'not_generated' | 'empty' | 'error'

export interface ContentState<T> {
  status: ContentLoadStatus
  data: T
  error?: string
}

export type KnowledgeType = 'entity' | 'concept' | 'case' | 'method' | 'insight'
export type RelationType = '相同' | '相似' | '补充' | '对比' | '延伸'

export interface RelationOverview {
  relation_overview: string
  related_video_count: number
  relation_count: number
  top_topics: string[]
}

export interface KnowledgeStructureField {
  key: string
  label: string
  value: string
}

export interface WikiDetailLink {
  title: string
  slug?: string
}

export interface CurrentKnowledgeAnchor {
  id: string
  knowledge_type: KnowledgeType
  content: string
  coreContent?: string
  structureFields?: KnowledgeStructureField[]
  evidenceIds?: string[]
  informationNature?: string
  timeRange?: string
  relatedKnowledge?: WikiDetailLink[]
  relatedEntities?: WikiDetailLink[]
  timestamp: string
  seconds: number
  related_count: number
}

export interface CrossVideoKnowledgeItem {
  id: string
  anchorId: string
  knowledge_type: KnowledgeType
  relation_type: RelationType
  knowledge_content: string
  timestamp: string
  seconds: number
  video_id: string
  video_title: string
  video_category: VideoCategory
  relation_description: string
}

export interface VideoData {
  id: string
  title: string
  category: VideoCategory
  categoryName: string
  status?: string
  initiallyAvailable?: boolean
  duration: string
  durationSeconds: number
  created_at: string
  video_url: string
  play_url?: string
  poster_url?: string
  cover_url?: string
  processing_error_summary?: string
  summarySource?: 'initial' | 'enhanced' | 'user_edited' | ''
  summaryKnowledgeEnhanced?: boolean
  summaryUserEdited?: boolean
  knowledgeAuditStatus?: 'passed' | 'conditional' | 'failed' | ''
  subtitle_file_url?: string
  overview: string
  chapters: Chapter[]
  subtitles: SubtitleCue[]
  summarySections?: SummarySection[]
  interviewSummary?: SummarySection[]
  trainingSummary?: SummarySection[]
  salonSummary?: SummarySection[]
  relationOverview?: RelationOverview
  currentAnchors?: CurrentKnowledgeAnchor[]
  crossVideoItems?: CrossVideoKnowledgeItem[]
}

export type VideoOption = Pick<VideoData, 'id' | 'title'>

export type VideoProcessingState = 'ready' | 'processing' | 'partial_completed' | 'completed' | 'failed'

export interface VideoProcessingFailure {
  job_id: string
  job_type: string
  category: string
  code: string
  message: string
  updated_at: string
}

export interface VideoProcessingJobStatus {
  job_id: string
  job_type: string
  transcript_generation?: string
  status: 'pending' | 'running' | 'succeeded' | 'failed' | 'cancelled'
  phase?: 'source_preparing' | 'tingwu_running'
  progress: number
  attempt_count: number
  max_attempts: number
  error_category?: string
  error_code?: string
  error_message?: string
  updated_at: string
  started_at?: string
  completed_at?: string
}

export interface VideoProcessingStatus {
  video_id: string
  status: VideoProcessingState
  foundation_status: VideoProcessingState
  enhancement_status: VideoProcessingState
  current_stage?: string
  transcript_generation?: string
  completed_stages: string[]
  failure?: VideoProcessingFailure
  enhancement_failure?: VideoProcessingFailure
  retryable_job?: { job_id: string; job_type: string }
  jobs: VideoProcessingJobStatus[]
  updated_at: string
}

export interface EvidenceLink {
  label: string
  timestamp: string
  seconds: number
  videoId?: string
  videoTitle?: string
}

export interface ChatMessage {
  id: string
  sender: 'user' | 'assistant'
  text: string
  timestamp: string
  relatedVideoId?: string
  relatedTime?: number
  relatedVideoTitle?: string
  evidenceLinks?: EvidenceLink[]
}

export interface ChatSession {
  id: string
  title: string
  type: 'chat' | 'doc' | 'video'
  time: string
  messages: ChatMessage[]
  scope?: 'global' | 'video'
  tenantId?: string
  videoId?: string
  videoTitle?: string
  videoCoverUrl?: string
}

export interface UploadForm {
  file: { name: string; size: number; raw?: File }
}

export interface UploadProgress {
  stage: 'uploading'
  percent: number
}

export interface GraphNode {
  id: string
  name: string
  label: string
  attributes: string[]
  video_id?: string
  video_title?: string
  video_category?: VideoCategory
  seconds?: number
  link_count?: number
  type?: string
  knowledge_id?: string
  evidence?: GraphEvidence[]
  knowledge_detail?: GraphKnowledgeDetail
}

export interface GraphKnowledgeDetail {
  id: string
  slug?: string
  title: string
  video_id?: string
  video_title?: string
  knowledge_type: KnowledgeType
  entity_sub_type?: 'person' | 'organization' | 'product' | 'technology' | 'industry' | 'place' | string
  page_type?: string
  core_content?: string
  structure_fields?: KnowledgeStructureField[]
  evidence_ids?: string[]
  information_nature?: string
  time_range?: string
  related_knowledge?: WikiDetailLink[]
  related_entities?: WikiDetailLink[]
}

export interface GraphEvidence {
  video_id: string
  video_title: string
  start_ms: number
  end_ms: number
  chunk_index: number
  chunk_ids?: string[]
}

export interface GraphEdge {
  id: string
  source: string
  target: string
  type: string
  weight?: number
  confidence?: number
}

export interface WikiGraphMeta {
  mode: 'overview' | 'ego'
  total: number
  returned: number
  truncated: boolean
  center?: string
  depth?: number
  familiar_count?: number
}

export interface WikiGraphRequest {
  mode?: 'overview' | 'ego'
  center?: string
  depth?: number
  types?: string[]
  limit?: number
  videoId?: string
}

export interface KnowledgeGraphPayload {
  nodes: GraphNode[]
  edges: GraphEdge[]
  wiki_pages?: GraphKnowledgeDetail[]
  meta: WikiGraphMeta
  attributes: string[]
}

export type DashboardRange = '7d' | '30d' | '90d' | 'custom'

export interface KpiSummary {
  total_questions: number
  active_videos: number
  cluster_count: number
  avg_questions_per_video: number
  trend: Record<'total_questions' | 'active_videos' | 'cluster_count' | 'avg_questions_per_video', number>
}

export interface QuestionTrendPoint {
  date: string
  count: number
  top_videos: Array<{ video_id: string; title: string; count: number }>
}

export interface QuestionClusterVideo {
  video_id: string
  title: string
  video_category: VideoCategory
  first_seconds: number
  first_timestamp: string
  deleted?: boolean
}

export interface QuestionCluster {
  id: string
  representative_question: string
  count: number
  related_video_count: number
  last_asked_at: string
  videos: QuestionClusterVideo[]
}

export interface DashboardPayload {
  range: DashboardRange
  from?: string
  to?: string
  kpi: KpiSummary
  trend: QuestionTrendPoint[]
  clusters: QuestionCluster[]
}

export interface DashboardRequest {
  range: DashboardRange
  from?: string
  to?: string
}
