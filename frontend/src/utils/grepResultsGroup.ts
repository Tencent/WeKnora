import type { GrepChunkResult, GrepKnowledgeResult } from '@/types/tool-results'

export type GrepGroupedRow = {
  key: string
  knowledge_id: string
  knowledge_base_id: string
  title: string
  is_faq: boolean
  chunk_hit_count: number
  title_match: boolean
  match_snippet: string
  chunks: { content: string; chunk_id: string; knowledge_id: string }[]
}

/** Collapse per-chunk grep hits into one row per document; FAQ entries stay separate. */
export function groupGrepChunkResults(chunkRows: GrepChunkResult[]): GrepGroupedRow[] {
  const map = new Map<string, GrepGroupedRow>()
  const order: string[] = []

  for (const result of chunkRows) {
    const isFAQ = !!result.faq_id || result.chunk_type === 'faq'
    const key = isFAQ ? (result.faq_id || result.chunk_id) : result.knowledge_id
    if (!key) continue

    const snippet = String(result.match_snippet ?? '').trim()
    if (!map.has(key)) {
      map.set(key, {
        key,
        knowledge_id: result.knowledge_id,
        knowledge_base_id: result.knowledge_base_id,
        title: result.faq_question || result.knowledge_title || '',
        is_faq: isFAQ,
        chunk_hit_count: 0,
        title_match: false,
        match_snippet: snippet,
        chunks: [],
      })
      order.push(key)
    }

    const group = map.get(key)!
    group.chunk_hit_count += 1
    if (result.title_match) group.title_match = true
    if (!group.match_snippet && snippet) group.match_snippet = snippet
    if (snippet) {
      group.chunks.push({
        content: snippet,
        chunk_id: result.faq_id || result.chunk_id,
        knowledge_id: result.knowledge_id,
      })
    }
  }

  return order.map((key) => map.get(key)!)
}

export function countGrepDocuments(toolData: {
  document_count?: number
  knowledge_results?: GrepKnowledgeResult[]
  chunk_results?: GrepChunkResult[]
} | null | undefined): number {
  if (!toolData) return 0
  if (typeof toolData.document_count === 'number' && toolData.document_count >= 0) {
    return toolData.document_count
  }
  if (toolData.knowledge_results?.length) {
    return toolData.knowledge_results.length
  }
  if (toolData.chunk_results?.length) {
    return groupGrepChunkResults(toolData.chunk_results).length
  }
  return 0
}
