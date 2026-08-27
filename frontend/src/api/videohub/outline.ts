import { get } from '@/utils/request'
import type { Chapter } from '@/types/videohub'
import { parseOutlineWikiPage } from './contentParsing'

interface WikiPageResponse {
  content?: string
}

export async function fetchOutline(videoId: string, durationSeconds = 0): Promise<Chapter[]> {
  const response: WikiPageResponse = await get(`/api/custom/videos/${videoId}/outline`)
  return parseOutlineWikiPage(response.content || '', durationSeconds)
}
