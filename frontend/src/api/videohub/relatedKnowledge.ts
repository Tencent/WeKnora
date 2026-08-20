import { MOCK_VIDEOS } from './mockVideos'
import type { CrossVideoKnowledgeItem, CurrentKnowledgeAnchor, RelationOverview } from '@/types/videohub'

export interface RelatedKnowledgePayload {
  videoId: string
  overview: RelationOverview | null
  anchors: CurrentKnowledgeAnchor[]
  crossVideoItems: CrossVideoKnowledgeItem[]
}

export async function fetchRelatedKnowledge(videoId: string): Promise<RelatedKnowledgePayload> {
  const video = MOCK_VIDEOS.find(item => item.id === videoId)
  if (!video) throw new Error('视频不存在')
  return {
    videoId,
    overview: video.relationOverview ?? null,
    anchors: video.currentAnchors ?? [],
    crossVideoItems: video.crossVideoItems ?? [],
  }
}
