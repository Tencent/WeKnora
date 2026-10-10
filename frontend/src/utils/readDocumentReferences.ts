import type { KnowledgeChunksListData } from '../types/tool-results'
import { mergeDocumentReferences, type KnowledgeReferenceLike } from './referenceSources'

/** Read results retain citation IDs even when SSE/history omits chunk bodies. */
export function readDocumentReferences(
  data: KnowledgeChunksListData,
  output: string,
  fallbackTitle: string,
): KnowledgeReferenceLike[] {
  const knowledgeId = data.knowledge_id || data.document?.knowledge_id
  const title = data.faq_question || data.knowledge_title || data.document?.title || knowledgeId || fallbackTitle
  const fileName = data.document?.file_name
  if (data.chunks?.length) {
    return mergeDocumentReferences(data.chunks.map((item, index) => ({
      id: item.chunk_id || item.id || knowledgeId,
      knowledge_id: item.knowledge_id || knowledgeId,
      knowledge_title: title,
      knowledge_filename: fileName,
      knowledge_base_id: item.knowledge_base_id || data.knowledge_base_id,
      chunk_index: item.chunk_index ?? item.index ?? index + 1,
      chunk_type: item.chunk_type || (data.faq_question ? 'faq' : undefined),
      content: item.content,
    })))
  }
  if (!knowledgeId && !data.faq_id) return []
  return [{
    // A document ID identifies the card, never a chunk to request from the API.
    id: data.faq_id || knowledgeId,
    chunk_ids: data.chunk_ids,
    source_chunk_id: data.source_chunk_id || undefined,
    knowledge_id: knowledgeId,
    knowledge_title: title,
    knowledge_filename: fileName,
    knowledge_base_id: data.knowledge_base_id,
    chunk_type: data.faq_question ? 'faq' : undefined,
    content: output.includes('omitted from history') ? undefined : output || undefined,
  }]
}
