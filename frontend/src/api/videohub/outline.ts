import { get } from '@/utils/request'
import type { Chapter } from '@/types/videohub'
import { parseOutlineWikiPage } from './contentParsing'

interface WikiPageResponse {
  status?: string
  page_type?: string
  video_id?: string
  content?: string
}

export async function fetchOutline(videoId: string, durationSeconds = 0): Promise<Chapter[]> {
  const response: WikiPageResponse = await get(`/api/custom/videos/${videoId}/outline`)
  if (response.video_id && response.video_id !== videoId) throw new Error('章节数据与当前视频不匹配')
  if (response.page_type && response.page_type !== 'outline') throw new Error('章节数据类型无效')
  return parseOutlineWikiPage(response.content || '', durationSeconds)
}
