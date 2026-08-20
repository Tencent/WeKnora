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
  duration: string
  durationSeconds: number
  created_at: string
  video_url: string
  poster_url?: string
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
  file: { name: string; size: number }
}

export interface UploadProgress {
  stage: 'uploading'
  percent: number
}
