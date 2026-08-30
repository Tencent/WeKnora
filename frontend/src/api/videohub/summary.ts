import { get } from '@/utils/request'
import type { SummarySection, VideoCategory } from '@/types/videohub'
import { parseStructuredSummary, type StructuredSummaryResponse } from './contentParsing'

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
    summary?: StructuredSummaryResponse
    transcript_generation?: string
    artifact_version?: number
    summary_source?: 'initial' | 'enhanced' | 'user_edited' | ''
    summary_knowledge_enhanced?: boolean
    summary_user_edited?: boolean
  } = await get(`/api/custom/videos/${videoId}/summary`)
  if (!response.summary) throw new Error('智能总结未返回结构化内容')
  return {
    videoId,
    category,
    sections: parseStructuredSummary(response.summary, category),
    transcriptGeneration: response.transcript_generation,
    summaryVersion: response.artifact_version,
    source: response.summary_source || '',
    knowledgeEnhanced: response.summary_knowledge_enhanced,
    userEdited: response.summary_user_edited,
  }
}
