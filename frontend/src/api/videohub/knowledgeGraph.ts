import { get } from '@/utils/request'
import type { KnowledgeGraphPayload, WikiGraphRequest } from '@/types/videohub'

export async function fetchKnowledgeGraph(req: WikiGraphRequest = {}): Promise<KnowledgeGraphPayload> {
  if (req.limit !== undefined && (!Number.isFinite(req.limit) || req.limit <= 0)) throw new Error('图谱节点数量参数无效')
  const response = await get<{ success: boolean; data?: KnowledgeGraphPayload; error?: string }>('/api/custom/graph', {
    params: {
      limit: req.limit,
      types: req.types?.join(','),
      video_id: req.videoId,
    },
  })
  if (!response.success || !response.data) throw new Error(response.error || '知识图谱加载失败')
  return response.data
}
