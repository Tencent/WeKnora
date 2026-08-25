export type VideoCategory = 'interview' | 'training' | 'salon' | 'general'

export interface KnowledgePoint {
  id: string
  title: string
  timestamp: string
  seconds: number
  transcriptSnippet?: string
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
}

export interface SubtitleCue {
  start_seconds: number
  end_seconds: number
  text: string
}

export interface EvidenceRef {
  evidenceTimestamp?: string
  evidenceSeconds?: number
  transcriptSnippet?: string
}

export interface SummarySection extends EvidenceRef {
  id: string
  title: string
  content: string
}

export type KnowledgeType = 'entity' | 'concept' | 'case' | 'method' | 'insight'
export type RelationType = '相同' | '相似' | '补充' | '对比' | '延伸'

export interface RelationOverview {
  relation_overview: string
  related_video_count: number
  relation_count: number
  top_topics: string[]
}

export interface CurrentKnowledgeAnchor {
  id: string
  knowledge_type: KnowledgeType
  content: string
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
  poster_url?: string
  processing_error_summary?: string
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

export interface EvidenceLink {
  label: string
  timestamp: string
  seconds: number
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
  type: 'chat' | 'doc'
  time: string
  messages: ChatMessage[]
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
