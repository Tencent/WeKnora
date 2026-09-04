import { get } from '@/utils/request'
import type { Chapter } from '@/types/videohub'
import { parseOutlineResponse, type CanonicalOutlineResponse } from './contentParsing'

export interface WikiPageResponse {
  status?: string
  page_type?: string
  video_id?: string
  schema_version?: number
  chapters?: CanonicalOutlineResponse['chapters']
  content?: string
  partial?: boolean
  completed_chapters?: number
  total_chapters?: number
  next_chapter_index?: number
}

export interface OutlineResult {
  chapters: Chapter[]
  partial: boolean
  completedChapters: number
  totalChapters: number
  nextChapterIndex: number
}

export async function fetchOutlineResult(videoId: string, durationSeconds = 0): Promise<OutlineResult> {
  const response: WikiPageResponse = await get(`/api/custom/videos/${videoId}/outline`)
  if (response.video_id && response.video_id !== videoId) throw new Error('章节数据与当前视频不匹配')
  if (response.page_type && response.page_type !== 'outline') throw new Error('章节数据类型无效')
  const chapters = parseOutlineResponse(response, durationSeconds)
  const completedChapters = Number.isInteger(response.completed_chapters) ? response.completed_chapters! : chapters.length
  const totalChapters = Number.isInteger(response.total_chapters) ? response.total_chapters! : completedChapters
  return {
    chapters,
    partial: response.partial === true || response.status === 'partial',
    completedChapters,
    totalChapters: Math.max(totalChapters, completedChapters),
    nextChapterIndex: Number.isInteger(response.next_chapter_index) ? response.next_chapter_index! : completedChapters + 1,
  }
}

export async function fetchOutline(videoId: string, durationSeconds = 0): Promise<Chapter[]> {
  return (await fetchOutlineResult(videoId, durationSeconds)).chapters
}
