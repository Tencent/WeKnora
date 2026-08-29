import { get } from '@/utils/request'
import type { SummarySection, VideoCategory } from '@/types/videohub'
import { parseSummaryWikiPage } from './contentParsing'

export interface SummaryResponse {
  videoId: string
  category: VideoCategory
  sections: SummarySection[]
  transcriptGeneration?: string
  summaryVersion?: number
  source?: 'initial' | 'enhanced' | 'user_edited' | ''
  knowledgeEnhanced?: boolean
  userEdited?: boolean
}

export async function fetchSummary(videoId: string, category: VideoCategory): Promise<SummaryResponse> {
  const response: {
    content?: string
    transcript_generation?: string
    artifact_version?: number
    summary_source?: 'initial' | 'enhanced' | 'user_edited' | ''
    summary_knowledge_enhanced?: boolean
    summary_user_edited?: boolean
  } = await get(`/api/custom/videos/${videoId}/summary`)
  return {
    videoId,
    category,
    sections: parseSummaryWikiPage(response.content || ''),
    transcriptGeneration: response.transcript_generation,
    summaryVersion: response.artifact_version,
    source: response.summary_source || '',
    knowledgeEnhanced: response.summary_knowledge_enhanced,
    userEdited: response.summary_user_edited,
  }
}
