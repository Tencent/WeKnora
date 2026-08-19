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
  summarySections?: unknown[]
  relationOverview?: unknown
  currentAnchors?: unknown[]
  crossVideoItems?: unknown[]
}

export type VideoOption = Pick<VideoData, 'id' | 'title'>
