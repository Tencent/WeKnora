import { get } from '@/utils/request'
import type { CrossVideoPayload, KnowledgeGraphDetailPayload, KnowledgeGraphPayload, WikiGraphRequest } from '@/types/videohub'

function unwrap<T>(response: unknown, fallback: string): T {
  const value = response as { success?: boolean; data?: T; error?: string; message?: string }
  if (value?.success === false) throw new Error(value.error || value.message || fallback)
  if (value?.data !== undefined) return value.data
  return response as T
}

export async function fetchKnowledgeGraph(req: WikiGraphRequest = {}): Promise<KnowledgeGraphPayload> {
  if (req.limit !== undefined && (!Number.isFinite(req.limit) || req.limit <= 0)) throw new Error('图谱节点数量参数无效')
  const response = await get<{ success: boolean; data?: KnowledgeGraphPayload; error?: string }>('/api/custom/graph', {
    params: {
      mode: req.mode,
      center: req.center,
      depth: req.depth,
      limit: req.limit,
      types: req.types?.join(','),
      video_id: req.videoId,
    },
  })
  return unwrap<KnowledgeGraphPayload>(response, '知识图谱加载失败')
}

export async function fetchKnowledgeGraphDetail(wikiPageId: string): Promise<KnowledgeGraphDetailPayload> {
  if (!wikiPageId.trim()) throw new Error('Wiki 页面身份缺失')
  const response = await get<{ success: boolean; data?: KnowledgeGraphDetailPayload; error?: string }>(`/api/custom/graph/wiki-pages/${encodeURIComponent(wikiPageId)}`)
  return unwrap<KnowledgeGraphDetailPayload>(response, '知识详情加载失败')
}

export async function fetchCrossVideoAssociations(videoId: string, wikiPageId?: string): Promise<CrossVideoPayload> {
  if (!videoId.trim()) throw new Error('来源视频身份缺失')
  const response = await get<{ success: boolean; data?: CrossVideoPayload; error?: string }>('/api/custom/graph/cross-video', {
    params: { video_id: videoId, wiki_page_id: wikiPageId },
  })
  return unwrap<CrossVideoPayload>(response, '跨视频关联加载失败')
}
