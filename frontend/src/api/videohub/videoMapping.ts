import type { VideoData, VideoCategory } from '@/types/videohub'

const CATEGORY_MAP: Record<string, { category: VideoCategory; name: string }> = {
  interview: { category: 'interview', name: '访谈' },
  tutorial: { category: 'training', name: '培训' },
  lecture: { category: 'general', name: '讲座' },
  case_analysis: { category: 'salon', name: '案例' },
}

const INITIAL_VIDEO_STATUSES = new Set(['uploaded', 'initializing', 'ready', 'processing', 'completed'])
export function isVideoInitiallyAvailable(video: { status?: string; file_url?: string; play_url?: string; thumbnail_url?: string; initially_available?: boolean }): boolean {
  if (video.initially_available === true) return true
  const status = video.status || ''
  return INITIAL_VIDEO_STATUSES.has(status) && Boolean((video.play_url || video.file_url)?.trim())
}

function formatDuration(seconds: number): string {
  if (!seconds || seconds <= 0) return '—'
  const minutes = Math.floor(seconds / 60)
  const remainder = Math.floor(seconds % 60)
  return minutes > 0 ? `${minutes}分${remainder}秒` : `${remainder}秒`
}

export function mapVideo(v: any, response?: any): VideoData {
  const cat = CATEGORY_MAP[v.video_type] || { category: 'general' as VideoCategory, name: v.video_type || '通用' }
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
    overview: '',
    chapters: [],
    subtitles: [],
    summarySections: [],
  }
}
