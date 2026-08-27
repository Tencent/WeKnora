import { get } from '@/utils/request'
import type { SummarySection, VideoCategory } from '@/types/videohub'
import { parseSummaryWikiPage } from './contentParsing'

export interface SummaryResponse {
  videoId: string
  category: VideoCategory
  sections: SummarySection[]
}

export async function fetchSummary(videoId: string, category: VideoCategory): Promise<SummaryResponse> {
  const response: { content?: string } = await get(`/api/custom/videos/${videoId}/summary`)
  return { videoId, category, sections: parseSummaryWikiPage(response.content || '') }
}
