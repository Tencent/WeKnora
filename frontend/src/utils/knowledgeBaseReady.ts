/** Model requirements are shared by card status, setup guidance and upload guards. */
export function isKnowledgeBaseReady(kb: {
  summary_model_id?: string;
  embedding_model_id?: string;
  indexing_strategy?: { vector_enabled?: boolean; keyword_enabled?: boolean };
} | null | undefined): boolean {
  if (!kb?.summary_model_id) return false;
  const strategy = kb.indexing_strategy;
  return !!strategy && !strategy.vector_enabled && !strategy.keyword_enabled || !!kb.embedding_model_id;
}
