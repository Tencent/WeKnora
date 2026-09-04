import type { VideoData, VideoCategory } from '@/types/videohub'

const CATEGORY_MAP: Record<string, { category: VideoCategory; name: string }> = {
  interview: { category: 'interview', name: '访谈' },
  training: { category: 'training', name: '培训' },
  salon: { category: 'salon', name: '研讨' },
  general: { category: 'general', name: '通用' },
  tutorial: { category: 'training', name: '培训' },
  lecture: { category: 'salon', name: '研讨' },
  case_analysis: { category: 'general', name: '通用' },
}

const INITIAL_VIDEO_STATUSES = new Set(['uploaded', 'initializing', 'ready', 'processing', 'completed', 'failed'])
export function isVideoInitiallyAvailable(video: { status?: string; file_url?: string; play_url?: string; thumbnail_url?: string; initially_available?: boolean }): boolean {
	if (typeof video.initially_available === 'boolean') return video.initially_available
  const status = video.status || ''
  return INITIAL_VIDEO_STATUSES.has(status) && Boolean((video.play_url || video.file_url)?.trim())
}

export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return '—'
  const totalSeconds = Math.floor(seconds)
  const hours = Math.floor(totalSeconds / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  const remainder = totalSeconds % 60
  return [hours, minutes, remainder].map(value => String(value).padStart(2, '0')).join(':')
}

export function mapVideo(v: any, response?: any): VideoData {
  const cat = CATEGORY_MAP[v.video_type] || { category: 'general' as VideoCategory, name: '通用' }
  const durationSeconds = Number(v.duration_seconds) || 0
  return {
    id: v.id,
    title: v.title,
    category: cat.category,
    categoryName: cat.name,
    status: v.status || '',
    initiallyAvailable: isVideoInitiallyAvailable({
      status: v.status,
      file_url: v.file_url,
      play_url: v.play_url,
      thumbnail_url: v.thumbnail_url,
      initially_available: v.initially_available ?? response?.initially_available,
    }),
    duration: formatDuration(durationSeconds),
    durationSeconds,
    created_at: v.created_at || '',
    video_url: v.play_url || v.file_url || '',
    play_url: v.play_url || v.file_url || '',
    poster_url: v.cover_url || v.thumbnail_url || '',
    cover_url: v.cover_url || v.thumbnail_url || '',
    processing_error_summary: v.processing_error_summary || '',
    summarySource: v.summary_source || '',
    summaryKnowledgeEnhanced: Boolean(v.summary_knowledge_enhanced),
    summaryUserEdited: Boolean(v.summary_user_edited),
    knowledgeAuditStatus: v.knowledge_audit_status || '',
    subtitle_file_url: v.subtitle_file_url || '',
    overview: '',
    chapters: [],
    subtitles: [],
    summarySections: [],
  }
}
