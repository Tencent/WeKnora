import { get } from '@/utils/request'
import type { CrossVideoKnowledgeItem, CurrentKnowledgeAnchor, RelationOverview } from '@/types/videohub'
import { mapRelatedKnowledgeResponse, type BackendRelatedKnowledgeResponse } from './contentParsing'

export interface RelatedKnowledgePayload {
  videoId: string
  overview: RelationOverview | null
  anchors: CurrentKnowledgeAnchor[]
  crossVideoItems: CrossVideoKnowledgeItem[]
}

export async function fetchRelatedKnowledge(videoId: string): Promise<RelatedKnowledgePayload> {
  const response: BackendRelatedKnowledgeResponse = await get(`/api/custom/videos/${videoId}/related-knowledge`)
  return mapRelatedKnowledgeResponse(videoId, response)
}
