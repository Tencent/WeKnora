import { get } from '@/utils/request'
import { parseTranscriptPageWikiPage } from './contentParsing'

export async function fetchTranscriptPage(videoId: string): Promise<string> {
  const response: { content?: string } = await get(`/api/custom/videos/${videoId}/transcript-page`)
  return parseTranscriptPageWikiPage(response.content || '')
}
