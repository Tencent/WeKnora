import { get } from '@/utils/request'
import { parseOverviewWikiPage } from './contentParsing'

export async function fetchOverview(videoId: string): Promise<string> {
  const response: { content?: string } = await get(`/api/custom/videos/${videoId}/overview`)
  return parseOverviewWikiPage(response.content || '')
}
