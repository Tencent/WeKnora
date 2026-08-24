import { get } from '@/utils/request'
import type { VideoData, VideoOption, VideoCategory } from '@/types/videohub'

// 后端 video_type → 前端 category 映射（后端分类：interview/tutorial/lecture/case_analysis）
const CATEGORY_MAP: Record<string, { category: VideoCategory; name: string }> = {
  interview: { category: 'interview', name: '访谈' },
  tutorial: { category: 'training', name: '培训' },
  lecture: { category: 'general', name: '讲座' },
  case_analysis: { category: 'salon', name: '案例' },
}

function formatDuration(seconds: number): string {
  if (!seconds || seconds <= 0) return '—'
  const m = Math.floor(seconds / 60)
  const s = Math.floor(seconds % 60)
  return m > 0 ? `${m}分${s}秒` : `${s}秒`
}

// 后端视频元数据 → 前端 VideoData（内容富字段后续由内容接口填充，先留空）
function mapVideo(v: any): VideoData {
  const cat = CATEGORY_MAP[v.video_type] || { category: 'general' as VideoCategory, name: v.video_type || '通用' }
  return {
    id: v.id,
    title: v.title,
    category: cat.category,
    categoryName: cat.name,
    status: v.status || '',
    duration: formatDuration(v.duration_seconds),
    durationSeconds: v.duration_seconds || 0,
    created_at: v.created_at || '',
    video_url: v.file_url || '',
    poster_url: v.thumbnail_url || '',
    processing_error_summary: v.processing_error_summary || '',
    overview: '',
    chapters: [],
    subtitles: [],
    summarySections: [],
  }
}

export async function fetchVideoList(): Promise<VideoData[]> {
  const resp: any = await get('/api/custom/videos')
  return (resp?.data || []).map(mapVideo)
}

export async function fetchVideoDetail(id: string): Promise<VideoData> {
  const resp: any = await get(`/api/custom/videos/${id}`)
  return mapVideo(resp?.data)
}

export async function fetchVideoOptions(): Promise<VideoOption[]> {
  const resp: any = await get('/api/custom/videos')
  return (resp?.data || []).map((v: any) => ({ id: v.id, title: v.title }))
}
