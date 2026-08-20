import { filterMockGraph } from './mockGraph'
import type { KnowledgeGraphPayload, WikiGraphRequest } from '@/types/videohub'

export async function fetchKnowledgeGraph(req: WikiGraphRequest = {}): Promise<KnowledgeGraphPayload> {
  await Promise.resolve()
  if (req.limit !== undefined && !Number.isFinite(req.limit)) return Promise.reject(new Error('图谱节点数量参数无效'))
  return filterMockGraph(req)
}
